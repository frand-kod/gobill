package billing

// Payment gateway settlement: paid recharges, top-ups and expired payments.

import (
	"database/sql"

	"context"
	"github.com/frand-kod/gobill/internal/db"
)

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

// RechargePaid is Recharge for a confirmed online payment. claim runs first in the SAME
// transaction (it flips payment_requests pending->paid); false means someone else already did,
// so nothing is recharged. A crash can therefore never leave "paid" without the recharge.
// With a coupon, price (what was charged) is recorded and the coupon usage is claimed; a coupon
// that ran out meanwhile does not fail the recharge, the customer has already paid.
func (s *Service) RechargePaid(ctx context.Context, claim func(*db.Queries) (bool, error), customerID, planID int64, method, coupon string, price int64) error {
	var p *pending
	err := s.tx(ctx, func(q *db.Queries) error {
		if ok, err := claim(q); err != nil || !ok {
			return err
		}
		if c, err := q.GetCustomer(ctx, customerID); err == nil && c.Status != "Active" {
			// PHP dies here and the payment is left in limbo. It is already claimed, so keep the
			// money: credit the customer's balance, to be spent once the account is active again.
			return creditPaid(ctx, q, s.now().Unix(), customerID, price, method, "Payment Credit")
		}
		var cp *couponUse
		if price > 0 { // record what the gateway charged (coupon discount and tax included)
			cp = &couponUse{price: price, mode: modeTotal}
		}
		if coupon != "" {
			if c, err := q.GetCouponByCode(ctx, coupon); err == nil {
				_, _ = q.UseCoupon(ctx, c.ID)
				if cp != nil {
					cp.code = c.Code
				}
			}
		}
		var err error
		p, err = s.recharge(ctx, q, customerID, planID, method, 0, cp)
		return err
	})
	if err != nil || p == nil {
		return err
	}
	s.apply(ctx, p)
	return nil
}

// ExpirePayments marks pending online payments past their expiry as expired.
func (s *Service) ExpirePayments(ctx context.Context) error {
	_, err := s.Q.ExpirePaymentRequests(ctx, s.now().Unix())
	return err
}
