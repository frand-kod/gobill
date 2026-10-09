package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

var ErrBadDeposit = errors.New("choose a Balance plan or enter an amount above 0")

// Deposit is the admin top-up (old plan/deposit): add to the balance by Balance plan (planID > 0,
// its price is the amount) or by a free amount. A transaction and an activity log are written and
// the recharge notification is sent.
func (s *Service) Deposit(ctx context.Context, customerID, planID, amount int64, note string, adminID int64) error {
	var p *pending
	err := s.tx(ctx, func(q *db.Queries) error {
		c, err := q.GetCustomer(ctx, customerID)
		if err != nil {
			return err
		}
		plan := db.Plan{Name: "Balance", Type: "Balance"}
		if planID > 0 {
			if plan, err = q.GetPlan(ctx, planID); err != nil {
				return err
			}
			amount = plan.Price
		}
		if plan.Type != "Balance" || amount <= 0 {
			return ErrBadDeposit
		}
		invoice, err := nextInvoice(ctx, q)
		if err != nil {
			return err
		}
		now := s.now()
		trx := db.CreateTransactionParams{Invoice: invoice, CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, PlanID: sql.NullInt64{Int64: planID, Valid: planID > 0},
			Username: c.Username, PlanName: plan.Name, RouterName: "balance", Type: "Balance", Price: amount,
			Method: "Admin - Deposit", Note: note, AdminID: nullID(adminID), PeriodStart: now.Unix(), PeriodEnd: now.Unix()}
		if _, err = q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: amount, ID: c.ID}); err != nil {
			return err
		}
		if _, err = q.CreateTransaction(ctx, trx); err != nil {
			return err
		}
		plan.Price = amount
		p = &pending{cust: c, plan: plan, trx: trx, expiry: now}
		return s.logAdmin(ctx, q, adminID, "deposit", fmt.Sprintf("%s %s +%d", invoice, c.Username, amount))
	})
	if err == nil {
		s.notifyRecharge(p)
	}
	return err
}

func (s *Service) logAdmin(ctx context.Context, q *db.Queries, adminID int64, action, desc string) error {
	return q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "admin", ActorID: adminID, Action: action, Description: desc})
}

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
		err = q.UpdateSubscription(ctx, db.UpdateSubscriptionParams{PlanID: cur.ID, RouterID: cur.RouterID, Type: cur.Type,
			ExpiresAt: expires.Unix(), Status: status, AdminID: nullID(adminID), ID: id})
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

func (s *Service) removeFromDevice(ctx context.Context, c db.Customer, p db.Plan) {
	dev, dc, dp, _, err := s.prepare(ctx, c, p)
	if err == nil {
		err = dev.RemoveCustomer(ctx, dc, dp)
	}
	if err != nil {
		slog.Error("device: remove failed, sync manually", "customer", c.Username, "plan", p.Name, "err", err)
	}
}

// ExtendSubscription adds days to the current expiry (old plan/extend).
func (s *Service) ExtendSubscription(ctx context.Context, id int64, days int, adminID int64) error {
	sub, err := s.Q.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	return s.EditSubscription(ctx, id, sub.PlanID, time.Unix(sub.ExpiresAt, 0).In(s.now().Location()).AddDate(0, 0, days), adminID)
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
	return s.logAdmin(ctx, s.Q, adminID, "subscription.deactivate", c.Username+" "+plan.Name)
}
