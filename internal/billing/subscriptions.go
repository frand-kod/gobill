package billing

// Subscription edit, extend, deactivate, expiry and renewal.

import (
	"database/sql"
	"log/slog"

	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"strconv"
	"time"
)

// EditSubscription changes a subscription's plan and/or expiry (old plan/edit). The status follows
// the expiry. When the plan changes, or the subscription goes live or dead, the device is synced
// after commit; a device failure is logged only, like Recharge.
func (s *Service) EditSubscription(ctx context.Context, id, planID int64, expires time.Time, adminID int64) error {
	var old, cur db.Plan
	var cust db.Customer
	status := "expired"
	err := s.tx(ctx, func(q *db.Queries) error {
		sub, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		if old, err = q.GetPlan(ctx, sub.PlanID); err != nil {
			return err
		}
		if cur, err = q.GetPlan(ctx, planID); err != nil {
			return err
		}
		if cur.Type == "Balance" {
			return errors.New("plan has no subscription")
		}
		if expires.Unix() < sub.StartedAt {
			return errors.New("expiry is before the start")
		}
		if cust, err = q.GetCustomer(ctx, sub.CustomerID); err != nil {
			return err
		}
		if expires.After(s.now()) {
			status = "active"
		}
		// Coming back from dead (expired/deactivated): PHP zeroes the data usage on expiry, so the
		// usage window restarts now. An extension of a live subscription keeps its usage.
		revive := status == "active" && (sub.Status != "active" || sub.ExpiresAt <= s.now().Unix())
		err = q.UpdateSubscription(ctx, db.UpdateSubscriptionParams{PlanID: cur.ID, RouterID: cur.RouterID, Type: cur.Type,
			ExpiresAt: expires.Unix(), Status: status, AdminID: nullID(adminID), ID: id})
		if err == nil && revive {
			err = q.RestartSubscription(ctx, db.RestartSubscriptionParams{StartedAt: s.now().Unix(), ID: id})
		}
		if err != nil {
			return err
		}
		return s.logAdmin(ctx, q, adminID, "subscription.update", fmt.Sprintf("%s %s until %s", cust.Username, cur.Name,
			expires.In(s.now().Location()).Format("2006-01-02 15:04")))
	})
	if err != nil {
		return err
	}
	changed := old.ID != cur.ID
	if changed { // the old plan's profile must go
		s.removeFromDevice(ctx, cust, old)
	}
	if status == "active" {
		if err := s.activate(ctx, &pending{cust: cust, plan: cur, change: changed}); err != nil {
			slog.Error("device: activate failed, sync manually", "customer", cust.Username, "plan", cur.Name, "err", err)
		}
	} else if !changed {
		s.removeFromDevice(ctx, cust, cur)
	}
	return nil
}

// ExtendSubscription adds days to the expiry (old plan/extend). Like PHP, a subscription whose
// expiry has already passed restarts from NOW + days (and goes active again, back on the device);
// one that has not expired yet extends from its current expiry.
func (s *Service) ExtendSubscription(ctx context.Context, id int64, days int, adminID int64) error {
	sub, err := s.Q.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	from, now := time.Unix(sub.ExpiresAt, 0).In(s.now().Location()), s.now()
	if !from.After(now) {
		from = now
	}
	return s.EditSubscription(ctx, id, sub.PlanID, from.AddDate(0, 0, days), adminID)
}

// DeactivateSubscription expires the subscription now and takes it off the router. Doing it twice
// is a no-op. Like cron expiry, the router goes first: if it fails nothing changes.
func (s *Service) DeactivateSubscription(ctx context.Context, id, adminID int64) error {
	sub, err := s.Q.GetSubscription(ctx, id)
	if err != nil || sub.Status != "active" {
		return err
	}
	plan, err := s.Q.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return err
	}
	c, err := s.Q.GetCustomer(ctx, sub.CustomerID)
	if err != nil {
		return err
	}
	dev, dc, dp, _, err := s.prepare(ctx, c, plan)
	if err != nil {
		return err
	}
	if err := dev.RemoveCustomer(ctx, dc, dp); err != nil {
		return fmt.Errorf("remove from router: %w", err)
	}
	n, err := s.Q.DeactivateSubscription(ctx, db.DeactivateSubscriptionParams{Now: s.now().Unix(), ID: id})
	if err != nil || n == 0 {
		return err
	}
	who := "system"
	if a, err := s.Q.GetAdmin(ctx, adminID); err == nil {
		who = a.Username
	}
	s.telegram("Admin " + who + " Deactivate " + plan.Name + " for u" + c.Username) // customers.php:268
	return s.logAdmin(ctx, s.Q, adminID, "subscription.deactivate", c.Username+" "+plan.Name)
}

// ExtendExpired is home.php ?extend=: an expired subscription is switched back on for
// extend_days days from now, once per calendar month per customer. (The old code kept only the
// month number in a cache file; the year is kept here too so it does not block a year later.)
func (s *Service) ExtendExpired(ctx context.Context, customerID, subID int64) (time.Time, error) {
	var p pending
	var until time.Time
	err := s.tx(ctx, func(q *db.Queries) error {
		c, err := q.GetCustomer(ctx, customerID)
		if err != nil {
			return err
		}
		if c.Status != "Active" {
			return ErrInactive
		}
		days, _ := strconv.Atoi(setting(ctx, q, "extend_days"))
		if !settingOn(setting(ctx, q, "extend_expired")) || days <= 0 {
			return ErrExtendDisabled
		}
		sub, err := q.GetSubscription(ctx, subID)
		if err != nil || sub.CustomerID != customerID {
			return ErrPlanNotFound
		}
		now := s.now()
		key, month := fmt.Sprint("extend_last_", customerID), now.Format("2006-01")
		if setting(ctx, q, key) == month {
			return ErrExtendAlready
		}
		if sub.Status == "active" {
			return ErrNotExpired
		}
		plan, err := q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return err
		}
		until = now.AddDate(0, 0, days)
		if err = q.UpdateSubscription(ctx, db.UpdateSubscriptionParams{PlanID: sub.PlanID, RouterID: sub.RouterID, Type: sub.Type,
			ExpiresAt: until.Unix(), Status: "active", AdminID: sub.AdminID, ID: sub.ID}); err != nil {
			return err
		}
		if err = q.RestartSubscription(ctx, db.RestartSubscriptionParams{StartedAt: now.Unix(), ID: sub.ID}); err != nil { // usage window restarts (R4)
			return err
		}
		// ponytail: one settings row per customer that has extended (extend_last_<id>). Ceiling: the
		// settings table grows with customers; move to a customers column once a migration is allowed.
		if err = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: key, Value: month}); err != nil {
			return err
		}
		if err = q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "customer", ActorID: c.ID, Action: "extend",
			Description: fmt.Sprintf("%s extend for %d days", c.Username, days)}); err != nil {
			return err
		}
		p = pending{cust: c, plan: plan, change: true, expiry: until}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	if err := s.activate(ctx, &p); err != nil {
		slog.Error("device: extend activate failed, sync manually", "customer", p.cust.Username, "err", err)
	}
	if n := s.notifier(); n != nil {
		n.Go("telegram", func(c context.Context) error {
			return n.Telegram(c, fmt.Sprintf("#u%s (%s) #id%d #extend #%s \n%s\nNew Expired: %s", p.cust.Username, p.cust.Fullname, p.cust.ID, p.plan.Type, p.plan.Name, until.Format("2006-01-02 15:04:05")))
		})
	}
	return until, nil
}

// pendingStartText is what the customer and the recharge message show instead of a date while
// a start_on_first_login subscription has not been used yet.
const pendingStartText = "Mulai saat login pertama"

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// StartPending starts the customer's pending subscription at its first RADIUS login: the usage
// window and expiry run from now with the full plan validity. The conditional UPDATE makes
// concurrent first logins safe; only the first one starts it.
func (s *Service) StartPending(ctx context.Context, customerID int64) error {
	subs, err := s.Q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: customerID, Limit: 1000})
	if err != nil {
		return err
	}
	for _, sub := range subs {
		if sub.Status != "active" || sub.PendingStart == 0 {
			continue
		}
		plan, err := s.Q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return err
		}
		c, err := s.Q.GetCustomer(ctx, customerID)
		if err != nil {
			return err
		}
		now := s.now()
		exp := NewExpiry(now, int(plan.Validity), Unit(plan.ValidityUnit), Options{BillingDay: billingDay(c, plan)})
		_, err = s.Q.StartPendingSubscription(ctx, db.StartPendingSubscriptionParams{StartedAt: now.Unix(), ExpiresAt: exp.Unix(), ID: sub.ID})
		return err
	}
	return nil
}

// activeSub finds the customer's active subscription for (router, type); the partial unique
// index guarantees at most one.
func activeSub(ctx context.Context, q *db.Queries, customerID int64, routerID sql.NullInt64, typ string) (db.Subscription, bool, error) {
	subs, err := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: customerID, Limit: 1000})
	if err != nil {
		return db.Subscription{}, false, err
	}
	for _, sub := range subs {
		if sub.Status == "active" && sub.RouterID == routerID && sub.Type == typ {
			return sub, true, nil
		}
	}
	return db.Subscription{}, false, nil
}

// hadSub reports whether the customer ever had a subscription for (router, type), in any status.
func hadSub(ctx context.Context, q *db.Queries, customerID int64, routerID sql.NullInt64, typ string) (bool, error) {
	subs, err := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: customerID, Limit: 1000})
	for _, sub := range subs {
		if sub.RouterID == routerID && sub.Type == typ {
			return true, err
		}
	}
	return false, err
}

// billingDay: customer override, else the postpaid plan's day, else 0 (NewExpiry uses 20).
func billingDay(c db.Customer, p db.Plan) int {
	if c.BillingDay.Valid {
		return int(c.BillingDay.Int64)
	}
	if p.Billing == "postpaid" && p.BillingDay.Valid {
		return int(p.BillingDay.Int64)
	}
	return 0
}

// ExpireDue expires due subscriptions, takes them off the router and auto-renews from balance.
// Like cron.php, the router is touched first: if it fails the row stays active and is retried
// next run. ExpireSubscription is the idempotent claim (0 rows = already expired).
func (s *Service) ExpireDue(ctx context.Context) error {
	if mins, _ := strconv.Atoi(setting(ctx, s.Q, "expired_notify_minutes_before")); mins > 0 {
		if err := s.noticeBeforeExpiry(ctx, mins); err != nil {
			slog.Error("early expired notice", "err", err)
		}
	}
	subs, err := s.Q.ListExpiredActiveSubscriptions(ctx, s.now().Unix())
	if err != nil {
		return err
	}
	autoRenew := setting(ctx, s.Q, "enable_balance") != "no" // PHP config enable_balance
	for _, sub := range subs {
		if err := s.expireOne(ctx, sub, autoRenew); err != nil {
			slog.Error("expiry failed", "subscription", sub.ID, "err", err)
		}
	}
	return nil
}

// noticeBeforeExpiry sends the expired message minutes ahead of each plan end in the window, once per
// period. The plan still ends at its own time; expireOne then skips the notice that already went out.
func (s *Service) noticeBeforeExpiry(ctx context.Context, minutes int) error {
	if s.notifier() == nil {
		return nil
	}
	now := s.now()
	subs, err := s.Q.ListSubscriptionsExpiringUnnotified(ctx, db.ListSubscriptionsExpiringUnnotifiedParams{Now: now.Unix(),
		Until: now.Add(time.Duration(minutes) * time.Minute).Unix()})
	if err != nil {
		return err
	}
	for _, sub := range subs {
		c, err := s.Q.GetCustomer(ctx, sub.CustomerID)
		if err != nil {
			return err
		}
		plan, err := s.Q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return err
		}
		if err := s.sendExpiredNotice(ctx, sub, c, plan); err != nil {
			slog.Error("early expired notice", "subscription", sub.ID, "err", err)
		}
	}
	return nil
}

// sendExpiredNotice sends the expired message for sub's period, at most once. The conditional claim
// runs first, so a concurrent run or the expiry itself finds it taken and sends nothing.
func (s *Service) sendExpiredNotice(ctx context.Context, sub db.Subscription, c db.Customer, plan db.Plan) error {
	nf := s.notifier()
	if nf == nil {
		return nil
	}
	n, err := s.Q.ClaimExpiryNotice(ctx, db.ClaimExpiryNoticeParams{Now: s.now().Unix(), ID: sub.ID, ExpiresAt: sub.ExpiresAt})
	if err != nil || n == 0 {
		return err
	}
	v := s.packageVars(ctx, c.ID, plan.Price)
	v["expired_date"] = time.Unix(sub.ExpiresAt, 0).In(s.now().Location()).Format("2006-01-02 15:04:05")
	nf.Go("expired", func(ctx context.Context) error { return nf.Expired(ctx, c, plan.Name, v) })
	return nil
}

func (s *Service) expireOne(ctx context.Context, sub db.Subscription, autoRenew bool) error {
	// The job works from a snapshot: a renewal since then must not be expired.
	now := s.now().Unix()
	cur, err := s.Q.GetSubscription(ctx, sub.ID)
	if err != nil || cur.Status != "active" || cur.ExpiresAt > now {
		return err
	}
	sub = cur
	plan, err := s.Q.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return err
	}
	c, err := s.Q.GetCustomer(ctx, sub.CustomerID)
	if err != nil {
		return err
	}
	dev, dc, dp, _, err := s.prepare(ctx, c, plan)
	if err != nil {
		return err
	}
	if err := dev.RemoveCustomer(ctx, dc, dp); err != nil {
		return fmt.Errorf("remove from router: %w", err)
	}
	n, err := s.Q.ExpireSubscription(ctx, db.ExpireSubscriptionParams{ID: sub.ID, Now: now})
	if err != nil || n == 0 {
		return err
	}
	nf := s.notifier()
	if nf != nil {
		if err := s.sendExpiredNotice(ctx, sub, c, plan); err != nil { // skipped when the early notice already went out
			slog.Error("expired notice", "subscription", sub.ID, "err", err)
		}
		nf.Go("webhook", func(ctx context.Context) error {
			return nf.Webhook(ctx, "recharge.expired", map[string]any{"username": c.Username, "plan": plan.Name, "expires_at": sub.ExpiresAt})
		})
	}
	if autoRenew && c.AutoRenewal == 1 && c.Status == "Active" && c.Balance >= WithTax(settingsMap(ctx, s.Q), plan.Price)+s.BillsTotal(ctx, c.ID) {
		if err := s.RechargeWithBalance(ctx, c.ID, plan.ID, 0); err != nil {
			if nf != nil {
				txt := fmt.Sprintf("FAILED RENEWAL #cron\n\n#u.%s #buy #%s \n%s\nPrice: %d", c.Username, plan.Type, plan.Name, plan.Price)
				nf.Go("telegram", func(ctx context.Context) error { return nf.Telegram(ctx, txt) })
			}
			return fmt.Errorf("auto renewal: %w", err)
		}
	}
	return nil
}

// ExpiryJob is the job.Run body: it skips (with a warning) while the clock is untrusted, since
// expiring on a wrong clock would cut off paying customers.
func (s *Service) ExpiryJob(trusted func() bool) func(context.Context) error {
	return func(ctx context.Context) error {
		if !trusted() {
			slog.Warn("expiry skipped: system clock not trusted")
			return nil
		}
		_ = s.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "expiry_last_run", Value: s.now().Format(time.DateTime)}) // dashboard job monitor
		if err := s.ExpirePayments(ctx); err != nil {
			slog.Error("expire payments", "err", err)
		}
		return s.ExpireDue(ctx)
	}
}
