package billing

// Admin balance deposits.

import (
	"database/sql"

	"context"
	"errors"
	"fmt"
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
