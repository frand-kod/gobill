package billing

// Balance transfers and plan sharing between customers.

import (
	"database/sql"

	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"strconv"
)

var ErrFriendPlanDiffers = errors.New("Target has active plan, different with current plant.")

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

// TransferBalance moves amount from one customer to another (home.php send=balance) in one
// transaction: debit, credit and one transaction row per side. Notifications run after commit.
func (s *Service) TransferBalance(ctx context.Context, fromID int64, toUsername string, amount int64) error {
	var from, to db.Customer
	err := s.tx(ctx, func(q *db.Queries) error {
		if setting(ctx, q, "enable_balance") == "no" || setting(ctx, q, "allow_balance_transfer") != "yes" {
			return ErrTransferDisabled
		}
		var err error
		if from, err = q.GetCustomer(ctx, fromID); err != nil {
			return err
		}
		if from.Status != "Active" {
			return ErrInactive
		}
		if to, err = q.GetCustomerByUsername(ctx, toUsername); errors.Is(err, sql.ErrNoRows) {
			return ErrTargetNotFound
		} else if err != nil {
			return err
		}
		if to.ID == from.ID {
			return ErrSelfTransfer
		}
		min, _ := strconv.ParseInt(setting(ctx, q, "minimum_transfer"), 10, 64)
		if amount <= 0 || amount < min {
			return ErrBelowMinimum
		}
		if from.Balance < amount {
			return ErrInsufficientBalance
		}
		if from.Balance, err = q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: -amount, ID: from.ID}); err != nil {
			return err
		}
		if to.Balance, err = q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: amount, ID: to.ID}); err != nil {
			return err
		}
		now := s.now().Unix()
		for _, t := range []struct {
			c          db.Customer
			name, peer string
		}{{from, "Send Balance", to.Username}, {to, "Receive Balance", from.Username}} {
			inv, err := nextInvoice(ctx, q)
			if err != nil {
				return err
			}
			if _, err = q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: inv, CustomerID: sql.NullInt64{Int64: t.c.ID, Valid: true},
				Username: t.c.Username, PlanName: t.name, RouterName: "balance", Type: "Balance", Price: amount,
				Method: "Customer - Balance", Note: t.peer, PeriodStart: now, PeriodEnd: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if n := s.notifier(); n != nil {
		bal := strconv.FormatInt(amount, 10)
		n.Go("balance_send", func(c context.Context) error {
			return n.Custom(c, from, "balance_send", "Balance Notification", map[string]string{"name": to.Fullname + " (" + to.Username + ")", "balance": bal, "current_balance": strconv.FormatInt(from.Balance, 10)})
		})
		n.Go("balance_received", func(c context.Context) error {
			return n.Custom(c, to, "balance_received", "Balance Notification", map[string]string{"name": from.Fullname + " (" + from.Username + ")", "balance": bal, "current_balance": strconv.FormatInt(to.Balance, 10)})
		})
		n.Go("telegram", func(c context.Context) error {
			return n.Telegram(c, fmt.Sprintf("#u%s send balance to #u%s \n%d", from.Username, to.Username, amount))
		})
	}
	return nil
}
