package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func (e *env) setStatus(t *testing.T, status string) {
	t.Helper()
	if _, err := e.conn.Exec("UPDATE customers SET status = ? WHERE id = ?", status, e.cust.ID); err != nil {
		t.Fatal(err)
	}
}

// P1: no recharge path accepts a non-Active customer; a paid gateway payment becomes balance.
func TestNonActiveCustomerRefused(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.setStatus(t, "Suspended")
	e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: "V1", PlanID: e.day.ID})
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 50000, ID: e.cust.ID})
	for name, err := range map[string]error{
		"recharge": e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 1),
		"balance":  e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0),
		"voucher":  e.s.RedeemVoucher(ctx, "V1", e.cust.ID),
	} {
		if !errors.Is(err, ErrInactive) {
			t.Errorf("%s: err %v", name, err)
		}
	}
	if e.balance() != 50000 || len(e.subs(t)) != 0 {
		t.Fatalf("balance %d subs %d", e.balance(), len(e.subs(t)))
	}
	if v, _ := e.q.GetVoucherByCode(ctx, "V1"); v.Status == "used" {
		t.Fatal("voucher consumed")
	}
	// paid gateway callback: payment kept as balance, no subscription
	claimed := false
	claim := func(*db.Queries) (bool, error) { claimed = true; return true, nil }
	if err := e.s.RechargePaid(ctx, claim, e.cust.ID, e.day.ID, "Tripay - QRIS", "", 10000); err != nil || !claimed {
		t.Fatal(err, claimed)
	}
	if e.balance() != 60000 || len(e.subs(t)) != 0 {
		t.Fatalf("balance %d", e.balance())
	}
}
