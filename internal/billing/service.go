package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/device"
	"github.com/frand-kod/nuxbill-go/internal/notify"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrVoucherInvalid      = errors.New("voucher not valid or already used")
)

// Service ports phpnuxbill Package::rechargeUser and cron.php expiry.
type Service struct {
	DB  *sql.DB
	Q   *db.Queries
	Key []byte         // decrypts routers.password_enc and customers.secret_enc
	Loc *time.Location // zone used for day/month/billing-day math
	Now func() time.Time
	// N sends notifications; nil means none. Swap at runtime with Reload.
	N  *notify.Notifier
	mu sync.RWMutex
	// DeviceFor builds the driver for a plan; nil means the default (by plans.device).
	DeviceFor func(plan db.Plan, router db.Router) (device.Device, error)
	// RouterFor builds the connection for a router (ping, pool sync); nil means from the stored row.
	RouterFor func(router db.Router) (device.Router, error)
}

// Reload swaps the notifier and time zone (after settings change).
func (s *Service) Reload(n *notify.Notifier, loc *time.Location) {
	s.mu.Lock()
	s.N, s.Loc = n, loc
	s.mu.Unlock()
}

func (s *Service) notifier() *notify.Notifier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.N
}

func (s *Service) now() time.Time {
	t := time.Now()
	if s.Now != nil {
		t = s.Now()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.Loc != nil {
		t = t.In(s.Loc)
	}
	return t
}

// pending is the router work to do once the DB transaction has committed.
type pending struct {
	cust   db.Customer
	plan   db.Plan
	change bool // plan change: PPPoE sessions are dropped so the new profile applies
	trx    db.CreateTransactionParams
	first  bool // first activation (no active subscription before)
	expiry time.Time
}

// Recharge activates/extends planID for the customer and records the transaction.
// method is the full PHP "gateway - channel" string, e.g. "Admin - Cash". adminID 0 = none.
//
// The router call runs AFTER commit. PHP calls the device first and only logs/Telegrams a
// failure, then writes the DB anyway; here a failed device call is likewise logged and does not
// fail the recharge (the customer has paid), but doing it after commit means a DB rollback can
// never leave the router changed for a recharge that did not happen.
func (s *Service) Recharge(ctx context.Context, customerID, planID int64, method string, adminID int64) error {
	var p *pending
	err := s.tx(ctx, func(q *db.Queries) (err error) {
		p, err = s.recharge(ctx, q, customerID, planID, method, adminID, nil)
		return
	})
	if err != nil {
		return err
	}
	s.apply(ctx, p)
	return nil
}

// RechargeWithBalance pays the plan price from the customer's balance, atomically.
func (s *Service) RechargeWithBalance(ctx context.Context, customerID, planID, adminID int64) error {
	var p *pending
	err := s.tx(ctx, func(q *db.Queries) error {
		plan, err := q.GetPlan(ctx, planID)
		if err != nil {
			return err
		}
		if plan.Type == "Balance" {
			return errors.New("cannot pay a balance top-up from balance")
		}
		c, err := q.GetCustomer(ctx, customerID)
		if err != nil {
			return err
		}
		if c.Balance < plan.Price {
			return ErrInsufficientBalance
		}
		// CHECK (balance >= 0) is the backstop against a concurrent overdraw.
		if _, err := q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: -plan.Price, ID: customerID}); err != nil {
			return fmt.Errorf("debit balance: %w", err)
		}
		p, err = s.recharge(ctx, q, customerID, planID, "Customer - Balance", adminID, nil)
		return err
	})
	if err != nil {
		return err
	}
	s.apply(ctx, p)
	return nil
}

// RedeemVoucher claims the voucher once, then recharges its plan (method "Voucher - CODE").
func (s *Service) RedeemVoucher(ctx context.Context, code string, customerID int64) error {
	var p *pending
	err := s.tx(ctx, func(q *db.Queries) error {
		v, err := q.GetVoucherByCode(ctx, code)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrVoucherInvalid
		} else if err != nil {
			return err
		}
		n, err := q.UseVoucher(ctx, db.UseVoucherParams{UsedBy: sql.NullInt64{Int64: customerID, Valid: true}, ID: v.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrVoucherInvalid
		}
		p, err = s.recharge(ctx, q, customerID, v.PlanID, "Voucher - "+code, 0, nil)
		return err // a failed recharge rolls the claim back too
	})
	if err != nil {
		return err
	}
	s.apply(ctx, p)
	return nil
}

func (s *Service) tx(ctx context.Context, fn func(*db.Queries) error) error {
	t, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer t.Rollback()
	if err := fn(s.Q.WithTx(t)); err != nil {
		return err
	}
	return t.Commit()
}

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id > 0} }

func setting(ctx context.Context, q *db.Queries, key string) string {
	rows, err := q.ListSettings(ctx)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.Key == key {
			return r.Value
		}
	}
	return ""
}

// recharge is the DB half of rechargeUser; it runs inside the caller's transaction.
func (s *Service) recharge(ctx context.Context, q *db.Queries, customerID, planID int64, method string, adminID int64, cp *couponUse) (*pending, error) {
	plan, err := q.GetPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("plan %d: %w", planID, err)
	}
	c, err := q.GetCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("customer %d: %w", customerID, err)
	}
	now := s.now()

	invoice, err := nextInvoice(ctx, q)
	if err != nil {
		return nil, err
	}
	trx := db.CreateTransactionParams{
		Invoice: invoice, CustomerID: sql.NullInt64{Int64: customerID, Valid: true},
		PlanID: sql.NullInt64{Int64: plan.ID, Valid: true}, Username: c.Username, PlanName: plan.Name,
		Type: plan.Type, Price: plan.Price, Method: method, AdminID: nullID(adminID),
		PeriodStart: now.Unix(), PeriodEnd: now.Unix(),
	}
	if cp != nil {
		trx.Price, trx.Note = cp.price, "Coupon "+cp.code
	}

	var pend *pending
	if plan.Type == "Balance" { // rechargeBalance: top up, no subscription, no router
		trx.RouterName = "balance"
		if _, err := q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: plan.Price, ID: customerID}); err != nil {
			return nil, err
		}
		pend = &pending{cust: c, plan: plan, trx: trx, first: false, expiry: now} // notify only, no router
	} else {
		rid := plan.RouterID // NULL for Radius plans: no router to name or touch
		if rid.Valid {
			router, err := q.GetRouter(ctx, rid.Int64)
			if err != nil {
				return nil, fmt.Errorf("router of plan %q: %w", plan.Name, err)
			}
			trx.RouterName = router.Name
		}

		active, found, err := activeSub(ctx, q, customerID, rid, plan.Type)
		if err != nil {
			return nil, err
		}
		extend := found && active.PlanID == plan.ID && setting(ctx, q, "extend_expiry") != "no"
		from, start := now, now.Unix()
		if extend {
			from, start = time.Unix(active.ExpiresAt, 0).In(now.Location()), active.StartedAt
		}
		exp := NewExpiry(from, int(plan.Validity), Unit(plan.ValidityUnit), Options{Extend: extend, BillingDay: billingDay(c, plan)})
		trx.PeriodStart, trx.PeriodEnd = start, exp.Unix()
		// PHP: first postpaid Period activation is billed 0; later periods bill the plan price.
		if plan.ValidityUnit == "Period" && !found {
			trx.Price = 0
		}

		if found {
			err = q.RenewSubscription(ctx, db.RenewSubscriptionParams{PlanID: plan.ID, RouterID: rid, Type: plan.Type,
				StartedAt: start, ExpiresAt: exp.Unix(), Method: method, AdminID: nullID(adminID), ID: active.ID})
		} else {
			_, err = q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: customerID, PlanID: plan.ID, RouterID: rid,
				Type: plan.Type, StartedAt: start, ExpiresAt: exp.Unix(), Method: method, AdminID: nullID(adminID)})
		}
		if err != nil {
			return nil, err
		}
		pend = &pending{cust: c, plan: plan, change: found && !extend, trx: trx, first: !found, expiry: exp}
	}

	if _, err := q.CreateTransaction(ctx, trx); err != nil {
		return nil, err
	}
	actor := "system"
	if adminID > 0 {
		actor = "admin"
	}
	err = q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: actor, ActorID: adminID, Action: "recharge",
		Description: fmt.Sprintf("%s %s %s (%s)", trx.Invoice, c.Username, plan.Name, method)})
	return pend, err
}

// nextInvoice is PHP _raid(): max(id)+1.
func nextInvoice(ctx context.Context, q *db.Queries) (string, error) {
	last, err := q.ListTransactions(ctx, db.ListTransactionsParams{Limit: 1})
	n := int64(1)
	if len(last) > 0 {
		n = last[0].ID + 1
	}
	return "INV-" + strconv.FormatInt(n, 10), err
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

// apply does the router side after commit. Failures are logged, not returned (see Recharge).
func (s *Service) apply(ctx context.Context, p *pending) {
	if p == nil {
		return
	}
	var err error
	if p.plan.Type != "Balance" {
		err = s.activate(ctx, p)
	}
	if err != nil {
		slog.Error("device: activate failed, sync manually", "customer", p.cust.Username, "plan", p.plan.Name, "err", err)
	}
	s.notifyRecharge(p)
}

func (s *Service) activate(ctx context.Context, p *pending) error {
	dev, dc, dp, router, err := s.prepare(ctx, p.cust, p.plan)
	if err == nil {
		if err = dev.AddCustomer(ctx, dc, dp); err == nil && p.change && p.plan.Type == "PPPoE" {
			err = dev.Disconnect(ctx, dc, router)
		}
	}
	return err
}

func (s *Service) notifyRecharge(p *pending) {
	n := s.notifier()
	if n == nil {
		return
	}
	const layout = "2006-01-02 15:04:05"
	loc := s.now().Location()
	gw, ch, _ := strings.Cut(p.trx.Method, " - ")
	vars := map[string]string{"invoice": p.trx.Invoice, "date": time.Unix(p.trx.PeriodStart, 0).In(loc).Format(layout),
		"payment_gateway": gw, "payment_channel": ch, "type": p.trx.Type, "plan_name": p.plan.Name,
		"plan_price": strconv.FormatInt(p.trx.Price, 10), "expired_date": p.expiry.In(loc).Format(layout)}
	data := map[string]any{"invoice": p.trx.Invoice, "username": p.cust.Username, "plan": p.plan.Name, "type": p.trx.Type,
		"price": p.trx.Price, "method": p.trx.Method, "router": p.trx.RouterName, "expires_at": p.expiry.Unix()}
	n.Go("recharge", func(ctx context.Context) error { return n.RechargeSuccess(ctx, p.cust, vars) })
	n.Go("webhook", func(ctx context.Context) error { return n.Webhook(ctx, "payment.paid", data) })
	if p.first {
		n.Go("webhook", func(ctx context.Context) error {
			return n.Webhook(ctx, "customer.activated", map[string]any{"username": p.cust.Username, "plan": p.plan.Name, "expires_at": p.expiry.Unix()})
		})
	}
}

// ExpireDue expires due subscriptions, takes them off the router and auto-renews from balance.
// Like cron.php, the router is touched first: if it fails the row stays active and is retried
// next run. ExpireSubscription is the idempotent claim (0 rows = already expired).
func (s *Service) ExpireDue(ctx context.Context) error {
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

func (s *Service) expireOne(ctx context.Context, sub db.Subscription, autoRenew bool) error {
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
	n, err := s.Q.ExpireSubscription(ctx, sub.ID)
	if err != nil || n == 0 {
		return err
	}
	nf := s.notifier()
	if nf != nil {
		v := map[string]string{"expired_date": time.Unix(sub.ExpiresAt, 0).In(s.now().Location()).Format("2006-01-02 15:04:05")}
		nf.Go("expired", func(ctx context.Context) error { return nf.Expired(ctx, c, plan.Name, v) })
		nf.Go("webhook", func(ctx context.Context) error {
			return nf.Webhook(ctx, "recharge.expired", map[string]any{"username": c.Username, "plan": plan.Name, "expires_at": sub.ExpiresAt})
		})
	}
	if autoRenew && c.AutoRenewal == 1 && c.Balance >= plan.Price {
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

// prepare resolves the driver and the device-side customer/plan for a plan's router.
func (s *Service) prepare(ctx context.Context, c db.Customer, p db.Plan) (dev device.Device, dc device.Customer, dp device.Plan, routerName string, err error) {
	var router db.Router // zero for Radius plans, whose device ignores it
	if p.RouterID.Valid {
		if router, err = s.Q.GetRouter(ctx, p.RouterID.Int64); err != nil {
			return
		}
	}
	mk := s.DeviceFor
	if mk == nil {
		mk = s.defaultDevice
	}
	if dev, err = mk(p, router); err != nil {
		return
	}
	dc = device.Customer{Username: c.Username, FullName: c.Fullname, Email: c.Email, Bills: []string{p.Name},
		PPPoEUsername: c.PppoeUsername, PPPoEIP: c.PppoeIp}
	if len(c.SecretEnc) > 0 {
		var pw []byte
		if pw, err = secret.Open(s.Key, c.SecretEnc); err != nil {
			err = fmt.Errorf("decrypt customer secret: %w", err)
			return
		}
		dc.Password = string(pw)
	}
	dp, err = s.devicePlan(ctx, p, true)
	return dev, dc, dp, router.Name, err
}

func (s *Service) devicePlan(ctx context.Context, p db.Plan, withExpired bool) (device.Plan, error) {
	dp := device.Plan{Name: p.Name, Type: p.Type, SharedUsers: int(p.SharedUsers.Int64), Limited: p.Limited == 1,
		LimitType: p.LimitType.String, TimeLimit: int(p.TimeLimit.Int64), TimeUnit: p.TimeUnit.String,
		DataLimit: int(p.DataLimit.Int64), DataUnit: p.DataUnit.String, OnLogin: p.OnLogin, OnLogout: p.OnLogout}
	if p.BandwidthID.Valid {
		b, err := s.Q.GetBandwidth(ctx, p.BandwidthID.Int64)
		if err != nil {
			return dp, err
		}
		dp.RateUp, dp.RateUpUnit, dp.RateDown, dp.RateDownUnit, dp.Burst = int(b.RateUp), b.RateUpUnit, int(b.RateDown), b.RateDownUnit, b.Burst
	}
	if p.PoolID.Valid {
		pl, err := s.Q.GetPool(ctx, p.PoolID.Int64)
		if err != nil {
			return dp, err
		}
		dp.Pool, dp.PoolLocalIP = pl.Name, pl.LocalIp
	}
	if withExpired && p.ExpiredPlanID.Valid {
		ep, err := s.Q.GetPlan(ctx, p.ExpiredPlanID.Int64)
		if err != nil {
			return dp, err
		}
		e, err := s.devicePlan(ctx, ep, false)
		if err != nil {
			return dp, err
		}
		dp.ExpiredPlan = &e
	}
	return dp, nil
}

func (s *Service) defaultDevice(p db.Plan, r db.Router) (device.Device, error) {
	if p.Device == "" || p.Device == "Dummy" {
		return device.Dummy{}, nil
	}
	if p.Device == "Radius" {
		return device.Radius{Q: s.Q, Key: s.Key}, nil
	}
	rt, err := s.routerConn(r)
	if err != nil {
		return nil, err
	}
	switch p.Device {
	case "MikrotikHotspot":
		return device.NewMikrotikHotspot(rt), nil
	case "MikrotikPppoe":
		return device.NewMikrotikPPPoE(rt), nil
	}
	return nil, fmt.Errorf("unknown device %q", p.Device)
}

// RechargePreview is what a recharge would do, computed without writing anything.
type RechargePreview struct {
	Plan    db.Plan
	Price   int64
	Expiry  time.Time // zero for Balance plans
	Extends bool
}

// Preview mirrors the expiry and price steps of recharge for the admin confirm page.
func (s *Service) Preview(ctx context.Context, customerID, planID int64) (RechargePreview, error) {
	plan, err := s.Q.GetPlan(ctx, planID)
	if err != nil {
		return RechargePreview{}, err
	}
	c, err := s.Q.GetCustomer(ctx, customerID)
	if err != nil {
		return RechargePreview{}, err
	}
	pv := RechargePreview{Plan: plan, Price: plan.Price}
	if plan.Type == "Balance" {
		return pv, nil
	}
	active, found, err := activeSub(ctx, s.Q, customerID, plan.RouterID, plan.Type)
	if err != nil {
		return pv, err
	}
	from := s.now()
	if pv.Extends = found && active.PlanID == plan.ID && setting(ctx, s.Q, "extend_expiry") != "no"; pv.Extends {
		from = time.Unix(active.ExpiresAt, 0).In(from.Location())
	}
	pv.Expiry = NewExpiry(from, int(plan.Validity), Unit(plan.ValidityUnit), Options{Extend: pv.Extends, BillingDay: billingDay(c, plan)})
	if plan.ValidityUnit == "Period" && !found {
		pv.Price = 0
	}
	return pv, nil
}
