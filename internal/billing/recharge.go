package billing

// Plan recharge flows (paid, zero-price, from balance), activation and previews.

import (
	"database/sql"
	"log/slog"

	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"strconv"
	"strings"
	"time"
)

// pending is the router work to do once the DB transaction has committed.
type pending struct {
	cust   db.Customer
	plan   db.Plan
	change bool // plan change: PPPoE sessions are dropped so the new profile applies
	trx    db.CreateTransactionParams
	trxID  int64
	first  bool // first activation (no active subscription before)
	expiry time.Time
	// start_on_first_login: expiry is provisional until the first RADIUS login (StartPending)
	pendingStart bool
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

// RechargeZero is the admin "Recharge Zero" (PHP using=zero): the plan is activated, nothing is
// charged and the transaction is recorded at price 0 (compensation, goodwill).
func (s *Service) RechargeZero(ctx context.Context, customerID, planID int64, method string, adminID int64) error {
	var p *pending
	err := s.tx(ctx, func(q *db.Queries) (err error) {
		p, err = s.recharge(ctx, q, customerID, planID, method, adminID, &couponUse{mode: modeZero})
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
		// The debit is what the transaction records (recharge decides the price), so it follows it.
		if p, err = s.recharge(ctx, q, customerID, planID, "Customer - Balance", adminID, nil); err != nil {
			return err
		}
		return debit(ctx, q, customerID, p.trx.Price)
	})
	if err != nil {
		return err
	}
	s.apply(ctx, p)
	return nil
}

// debit takes amount off the balance. CHECK (balance >= 0) is the backstop against a concurrent overdraw.
func debit(ctx context.Context, q *db.Queries, customerID, amount int64) error {
	c, err := q.GetCustomer(ctx, customerID)
	if err != nil {
		return err
	}
	if c.Balance < amount {
		return ErrInsufficientBalance
	}
	if _, err := q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: -amount, ID: customerID}); err != nil {
		return fmt.Errorf("debit balance: %w", err)
	}
	return nil
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
	if c.Status != "Active" { // PHP Package::rechargeUser dies for any other status
		return nil, ErrInactive
	}
	now := s.now()

	invoice, err := s.nextInvoice(ctx, q, now)
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
		if cp.mode == modeZero || cp.mode == modeTotal {
			trx.Price = cp.price // Recharge Zero, or what the gateway charged
		}
		if cp.code != "" {
			trx.Note = "Coupon " + cp.code
		}
	}

	var pend *pending
	if plan.Type == "Balance" { // rechargeBalance: top up, no subscription, no router
		trx.RouterName = "balance"
		if _, err := q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: trx.Price, ID: customerID}); err != nil {
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
		extend := found && active.PlanID == plan.ID && active.PendingStart == 0 && setting(ctx, q, "extend_expiry") != "no"
		pendStart := !rid.Valid && settingOn(setting(ctx, q, "start_on_first_login")) && (!found || active.PendingStart == 1)
		from, start := now, now.Unix()
		if extend {
			from, start = time.Unix(active.ExpiresAt, 0).In(now.Location()), active.StartedAt
		}
		exp := NewExpiry(from, int(plan.Validity), Unit(plan.ValidityUnit), Options{Extend: extend, BillingDay: billingDay(c, plan)})
		trx.PeriodStart, trx.PeriodEnd = start, exp.Unix()
		// PHP: only the very first Period activation (never had a recharge row, active or not) is
		// billed 0 and carries no bills. Otherwise: price (Invoice attr for Period, or the coupon-
		// discounted price) + tax on that + the customer's bills.
		var bills []billItem
		qt, err := quote(ctx, q, customerID, plan, cp)
		if err != nil {
			return nil, err
		}
		trx.Price, bills = qt.price, qt.bills
		if qt.note != "" {
			trx.Note = qt.note + trx.Note
		}

		if found {
			err = q.RenewSubscription(ctx, db.RenewSubscriptionParams{PlanID: plan.ID, RouterID: rid, Type: plan.Type,
				StartedAt: start, ExpiresAt: exp.Unix(), Method: method, AdminID: nullID(adminID), PendingStart: b2i(pendStart), ID: active.ID})
		} else {
			_, err = q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: customerID, PlanID: plan.ID, RouterID: rid,
				Type: plan.Type, StartedAt: start, ExpiresAt: exp.Unix(), Method: method, AdminID: nullID(adminID), PendingStart: b2i(pendStart)})
		}
		if err != nil {
			return nil, err
		}
		if err := payBills(ctx, q, customerID, bills); err != nil {
			return nil, err
		}
		if plan.ValidityUnit == "Period" && plan.Price != 0 {
			// Next invoice: the plan price, prorated by days after the first activation.
			inv := plan.Price
			if !found {
				days := int64(exp.Sub(now).Hours() / 24)
				if g := plan.Price * days / (30 * plan.Validity); g < inv {
					inv = g
				}
			}
			if err := setAttr(ctx, q, customerID, "Invoice", strconv.FormatInt(inv, 10)); err != nil {
				return nil, err
			}
		}
		pend = &pending{cust: c, plan: plan, change: found && !extend, trx: trx, first: !found, expiry: exp, pendingStart: pendStart}
	}

	t, err := q.CreateTransaction(ctx, trx)
	if err != nil {
		return nil, err
	}
	pend.trxID = t.ID
	actor := "system"
	if adminID > 0 {
		actor = "admin"
	}
	err = q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: actor, ActorID: adminID, Action: "recharge",
		Description: fmt.Sprintf("%s %s %s (%s)", trx.Invoice, c.Username, plan.Name, method)})
	return pend, err
}

// nextInvoice is PHP _raid(): max(id)+1, as INV-YYMM-NNNNNN where YYMM is now's month in the
// service zone. The sequence is global (not reset per month); it must run inside the write tx.
func (s *Service) nextInvoice(ctx context.Context, q *db.Queries, now time.Time) (string, error) {
	last, err := q.ListTransactions(ctx, db.ListTransactionsParams{Limit: 1})
	if err != nil {
		return "", err
	}
	n := int64(1)
	if len(last) > 0 {
		n = last[0].ID + 1
	}
	return fmt.Sprintf("INV-%s-%06d", now.In(s.Location()).Format("0601"), n), nil
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
		if f, ok := ctx.Value(deviceFailKey{}).(*bool); ok {
			*f = true
		}
		slog.Error("device: activate failed, sync manually", "customer", p.cust.Username, "plan", p.plan.Name, "err", err)
		s.telegram(fmt.Sprintf("System Error. When activate Package. You need to sync manually\nRouter: %s\nCustomer: u%s\nPlan: p%s\n%v",
			p.trx.RouterName, p.cust.Username, p.plan.Name, err))
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
	expired := p.expiry.In(loc).Format(layout)
	if p.pendingStart {
		expired = pendingStartText
	}
	vars := map[string]string{"invoice": p.trx.Invoice, "date": time.Unix(p.trx.PeriodStart, 0).In(loc).Format(layout),
		"payment_gateway": gw, "payment_channel": ch, "type": p.trx.Type, "plan_name": p.plan.Name,
		"plan_price": notify.Money(p.trx.Price), "price": notify.Money(p.trx.Price), "expired_date": expired,
		"trx_date": time.Unix(p.trx.PeriodStart, 0).In(loc).Format(layout), "note": p.trx.Note,
		"bills": p.trx.Note + "Total : " + notify.Money(p.trx.Price) + "\n", "invoice_link": fmt.Sprintf("/portal/orders/%d/invoice", p.trxID)}
	data := map[string]any{"invoice": p.trx.Invoice, "username": p.cust.Username, "plan": p.plan.Name, "type": p.trx.Type,
		"price": p.trx.Price, "method": p.trx.Method, "router": p.trx.RouterName, "expires_at": p.expiry.Unix()}
	if p.plan.Type != "Balance" { // Package.php #recharge (extend) / #buy (new)
		tag := "#recharge"
		if p.first {
			tag = "#buy"
		}
		s.telegram(fmt.Sprintf("#u%s %s %s #%s \n%s\nRouter: %s\nGateway: %s\nChannel: %s\nExpired: %s\nPrice: %s\nNote:\n%s",
			p.cust.Username, p.cust.Fullname, tag, p.plan.Type, p.plan.Name, p.trx.RouterName, gw, ch,
			p.expiry.In(loc).Format(layout), notify.Money(p.trx.Price), p.trx.Note))
	}
	n.Go("recharge", func(ctx context.Context) error { return n.RechargeSuccess(ctx, p.cust, vars) })
	n.Go("webhook", func(ctx context.Context) error { return n.Webhook(ctx, "payment.paid", data) })
	if p.first {
		n.Go("webhook", func(ctx context.Context) error {
			return n.Webhook(ctx, "customer.activated", map[string]any{"username": p.cust.Username, "plan": p.plan.Name, "expires_at": p.expiry.Unix()})
		})
	}
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
	qt, err := quote(ctx, s.Q, customerID, plan, nil)
	if err != nil {
		return pv, err
	}
	pv.Price = qt.price
	return pv, nil
}
