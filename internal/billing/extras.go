package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/frand-kod/gobill/internal/db"
)

var ErrFriendPlanDiffers = errors.New("Target has active plan, different with current plant.")

// activeSubs lists the customer's subscriptions with status active.
func (s *Service) activeSubs(ctx context.Context, customerID int64) ([]db.Subscription, error) {
	subs, err := s.Q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: customerID, Limit: 500})
	var out []db.Subscription
	for _, x := range subs {
		if x.Status == "active" {
			out = append(out, x)
		}
	}
	return out, err
}

// DeactivateCustomer expires every active subscription now and removes it from its device (old
// customers/deactivate, for all plans). It keeps going after a device failure and returns the
// number deactivated plus the joined errors.
func (s *Service) DeactivateCustomer(ctx context.Context, customerID, adminID int64) (int, error) {
	subs, err := s.activeSubs(ctx, customerID)
	n := 0
	for _, x := range subs {
		if e := s.DeactivateSubscription(ctx, x.ID, adminID); e != nil {
			err = errors.Join(err, e)
		} else {
			n++
		}
	}
	return n, err
}

// DeleteCustomer removes the customer and its recharges but keeps transactions (customer_id becomes
// NULL), like the old customers/delete. The router is contacted after the commit: a device failure
// is returned as an error but the delete stays.
func (s *Service) DeleteCustomer(ctx context.Context, id int64) error {
	c, err := s.Q.GetCustomer(ctx, id)
	if err != nil {
		return err
	}
	subs, err := s.activeSubs(ctx, id)
	if err != nil {
		return err
	}
	plans := make([]db.Plan, 0, len(subs))
	for _, x := range subs {
		p, err := s.Q.GetPlan(ctx, x.PlanID)
		if err != nil {
			return err
		}
		plans = append(plans, p)
	}
	if err := s.tx(ctx, func(q *db.Queries) error {
		if err := q.DetachCustomerTransactions(ctx, nullID(id)); err != nil {
			return err
		}
		return q.DeleteCustomer(ctx, id)
	}); err != nil {
		return err
	}
	var derr error
	for _, p := range plans {
		dev, dc, dp, _, e := s.prepare(ctx, c, p)
		if e == nil {
			e = dev.RemoveCustomer(ctx, dc, dp)
		}
		if e != nil {
			derr = errors.Join(derr, fmt.Errorf("remove from router: %w", e))
		}
	}
	return derr
}

// SyncCustomer re-sends every active subscription of the customer to its device.
func (s *Service) SyncCustomer(ctx context.Context, customerID, adminID int64) (int, error) {
	subs, err := s.activeSubs(ctx, customerID)
	n := 0
	for _, x := range subs {
		if e := s.SyncSubscription(ctx, x.ID, adminID); e != nil {
			err = errors.Join(err, e)
		} else {
			n++
		}
	}
	return n, err
}

// SendPlan is order/send (Buy for friend): fromID pays the plan price from their balance and the
// friend (an existing other username) gets the plan, in one transaction. Rules from the old code:
// enable_balance must not be off, the buyer must be Active, the friend must exist, not be the
// buyer, and must not hold an active plan other than this one. Router work and notifications
// run after commit.
func (s *Service) SendPlan(ctx context.Context, fromID int64, friend string, planID int64) error {
	var p *pending
	var from, to db.Customer
	err := s.tx(ctx, func(q *db.Queries) error {
		if setting(ctx, q, "enable_balance") == "no" {
			return ErrTransferDisabled
		}
		var err error
		if from, err = q.GetCustomer(ctx, fromID); err != nil {
			return err
		}
		if from.Status != "Active" {
			return ErrInactive
		}
		plan, err := q.GetPlan(ctx, planID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (plan.Enabled != 1 || plan.Type == "Balance" || plan.Billing != "prepaid") {
			return ErrPlanNotFound
		} else if err != nil {
			return err
		}
		if to, err = q.GetCustomerByUsername(ctx, friend); errors.Is(err, sql.ErrNoRows) {
			return ErrTargetNotFound
		} else if err != nil {
			return err
		}
		if to.ID == from.ID {
			return ErrSelfTransfer
		}
		subs, err := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: to.ID, Limit: 500})
		if err != nil {
			return err
		}
		for _, x := range subs {
			if x.Status == "active" && x.PlanID != plan.ID {
				return ErrFriendPlanDiffers
			}
		}
		if p, err = s.recharge(ctx, q, to.ID, plan.ID, "Balance - Gift from "+from.Username, 0, nil); err != nil {
			return err
		}
		if err = debit(ctx, q, from.ID, p.trx.Price); err != nil {
			return err
		}
		if from, err = q.GetCustomer(ctx, from.ID); err != nil {
			return err
		}
		inv, err := nextInvoice(ctx, q)
		if err != nil {
			return err
		}
		now := s.now().Unix()
		_, err = q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: inv, CustomerID: sql.NullInt64{Int64: from.ID, Valid: true},
			Username: from.Username, PlanName: "Send Plan: " + plan.Name, RouterName: "balance", Type: "Balance", Price: p.trx.Price,
			Method: "Customer - Balance", Note: to.Username, PeriodStart: now, PeriodEnd: now})
		return err
	})
	if err != nil {
		return err
	}
	s.apply(ctx, p) // friend: router + "recharge success" message
	if n := s.notifier(); n != nil {
		price := strconv.FormatInt(p.trx.Price, 10)
		n.Go("balance_send", func(c context.Context) error {
			return n.Custom(c, from, "balance_send", "Balance Notification", map[string]string{"name": to.Fullname + " (" + to.Username + ")",
				"balance": price, "current_balance": strconv.FormatInt(from.Balance, 10)})
		})
	}
	return nil
}

// TopUpPaid credits a paid custom-amount gateway payment to the balance (allow_balance_custom).
// claim runs first in the same transaction, like RechargePaid.
func (s *Service) TopUpPaid(ctx context.Context, claim func(*db.Queries) (bool, error), customerID, amount int64, method string) error {
	return s.tx(ctx, func(q *db.Queries) error {
		if ok, err := claim(q); err != nil || !ok {
			return err
		}
		return creditPaid(ctx, q, s.now().Unix(), customerID, amount, method, "Custom Balance")
	})
}

// creditPaid adds an already-paid amount to the balance and records it as a Balance transaction.
func creditPaid(ctx context.Context, q *db.Queries, now, customerID, amount int64, method, name string) error {
	c, err := q.GetCustomer(ctx, customerID)
	if err != nil {
		return err
	}
	if _, err = q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: amount, ID: customerID}); err != nil {
		return err
	}
	inv, err := nextInvoice(ctx, q)
	if err != nil {
		return err
	}
	_, err = q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: inv, CustomerID: sql.NullInt64{Int64: customerID, Valid: true},
		Username: c.Username, PlanName: name, RouterName: "balance", Type: "Balance", Price: amount, Method: method,
		PeriodStart: now, PeriodEnd: now})
	return err
}

// LogKinds are the log tables CleanLog knows.
var LogKinds = []string{"activity", "radius", "messages"}

// CleanLog deletes rows of one log kind older than days and returns how many. For radius only
// CLOSED sessions are removed: open ones are live data. days <= 0 does nothing.
func (s *Service) CleanLog(ctx context.Context, kind string, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	cut := s.now().AddDate(0, 0, -days).Unix()
	switch kind {
	case "activity":
		return s.Q.DeleteActivityLogsBefore(ctx, cut)
	case "radius":
		return s.Q.DeleteRadiusSessionsClosedBefore(ctx, sql.NullInt64{Int64: cut, Valid: true})
	case "messages":
		return s.Q.DeleteMessageLogsBefore(ctx, cut)
	}
	return 0, fmt.Errorf("unknown log kind %q", kind)
}

// LogCleanJob is the daily auto-clean (run it every few minutes; it acts once per day). It reads
// the setting log_keep_days; 0 or empty = keep forever.
func (s *Service) LogCleanJob(trusted func() bool) func(context.Context) error {
	var last string
	return func(ctx context.Context) error {
		day := s.now().Format("2006-01-02")
		if day == last || trusted != nil && !trusted() {
			return nil
		}
		days, _ := strconv.Atoi(setting(ctx, s.Q, "log_keep_days"))
		for _, k := range LogKinds {
			if _, err := s.CleanLog(ctx, k, days); err != nil {
				return err
			}
		}
		last = day
		return nil
	}
}
