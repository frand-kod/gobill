package billing

// Payment gateway settlement: paid recharges, top-ups and expired payments.

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"context"
	"github.com/frand-kod/gobill/internal/db"
	"time"
)

// TopUpPaid credits a paid custom-amount gateway payment to the balance (allow_balance_custom).
// claim runs first in the same transaction, like RechargePaid.
func (s *Service) TopUpPaid(ctx context.Context, claim func(*db.Queries) (bool, error), customerID, amount int64, method string) error {
	return s.tx(ctx, func(q *db.Queries) error {
		if ok, err := claim(q); err != nil || !ok {
			return err
		}
		return s.creditPaid(ctx, q, s.now(), customerID, amount, method, "Custom Balance")
	})
}

// creditPaid adds an already-paid amount to the balance and records it as a Balance transaction.
func (s *Service) creditPaid(ctx context.Context, q *db.Queries, now time.Time, customerID, amount int64, method, name string) error {
	c, err := q.GetCustomer(ctx, customerID)
	if err != nil {
		return err
	}
	if _, err = q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: amount, ID: customerID}); err != nil {
		return err
	}
	inv, err := s.nextInvoice(ctx, q, now)
	if err != nil {
		return err
	}
	_, err = q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: inv, CustomerID: sql.NullInt64{Int64: customerID, Valid: true},
		Username: c.Username, PlanName: name, RouterName: "balance", Type: "Balance", Price: amount, Method: method,
		PeriodStart: now.Unix(), PeriodEnd: now.Unix()})
	return err
}

// RechargePaid is Recharge for a confirmed online payment. claim runs first in the SAME
// transaction (it flips payment_requests pending->paid); false means someone else already did,
// so nothing is recharged. A crash can therefore never leave "paid" without the recharge.
// With a coupon, price (what was charged) is recorded; the coupon use was reserved at order time.
func (s *Service) RechargePaid(ctx context.Context, claim func(*db.Queries) (bool, error), customerID, planID int64, method, coupon string, price int64) error {
	var p *pending
	var credited string // set when the payment was kept as balance instead of a recharge
	err := s.tx(ctx, func(q *db.Queries) error {
		if ok, err := claim(q); err != nil || !ok {
			return err
		}
		if c, err := q.GetCustomer(ctx, customerID); err == nil && c.Status != "Active" {
			// PHP dies here and the payment is left in limbo. It is already claimed, so keep the
			// money: credit the customer's balance, to be spent once the account is active again.
			credited = "customer is not active"
			return s.creditPaid(ctx, q, s.now(), customerID, price, method, "Payment Credit")
		}
		var cp *couponUse
		if price > 0 { // record what the gateway charged (coupon discount and tax included)
			cp = &couponUse{price: price, mode: modeTotal}
		}
		if coupon != "" {
			if c, err := q.GetCouponByCode(ctx, coupon); err == nil { // the use was reserved when the order was created
				if cp != nil {
					cp.code = c.Code
				}
			}
		}
		var err error
		p, err = s.recharge(ctx, q, customerID, planID, method, 0, cp)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrIsolirPlan) {
			// The plan (or its router) is gone or can no longer be sold: retrying never helps and the
			// money is already claimed, so keep it as balance. recharge fails before it writes anything.
			p, credited = nil, fmt.Sprintf("plan %d cannot be recharged: %v", planID, err)
			return s.creditPaid(ctx, q, s.now(), customerID, price, method, "Payment Credit")
		}
		return err
	})
	if credited != "" && err == nil {
		msg := fmt.Sprintf("Pembayaran gateway diterima tetapi tidak bisa jadi paket (%s): dana masuk ke saldo pelanggan, cek manual\nPelanggan: %d\nMetode: %s\nJumlah: %d", credited, customerID, method, price)
		if aerr := s.Alert(ctx, msg); aerr != nil {
			slog.Error("operator alert", "customer", customerID, "err", aerr)
		}
	}
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
