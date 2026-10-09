package billing

import (
	"context"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func lastPrice(e *env) (price int64, note string) {
	e.conn.QueryRow("SELECT price, note FROM transactions ORDER BY id DESC LIMIT 1").Scan(&price, &note)
	return
}

// price = base (coupon-discounted plan price) + tax(base) + bills; the balance debit equals it.
func TestCombinedPrice(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	for k, v := range map[string]string{"enable_tax": "yes", "tax_rate": "10"} {
		e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v})
	}
	setAttr(ctx, e.q, e.cust.ID, "Router Bill", "500")
	setAttr(ctx, e.q, e.cust.ID, "Phone Bill", "300:2")
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 100000, ID: e.cust.ID})
	if pv, _ := e.s.Preview(ctx, e.cust.ID, e.day.ID); pv.Price != 11800 { // 10000 + 1000 + 800
		t.Fatalf("preview %d", pv.Price)
	}
	if got := e.s.OrderPrice(ctx, e.cust.ID, 10000); got != 11800 {
		t.Fatalf("order price %d", got)
	}
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); err != nil {
		t.Fatal(err)
	}
	if p, _ := lastPrice(e); p != 11800 || e.balance() != 100000-11800 {
		t.Fatalf("price %d balance %d", p, e.balance())
	}
	if got := attrs(ctx, e.q, e.cust.ID)["Phone Bill"]; got != "300:1" {
		t.Fatalf("installment %q", got)
	}
	// coupon: 2000 off the plan price only; tax on the discounted price, bills on top
	e.q.CreateCoupon(ctx, db.CreateCouponParams{Code: "C2", Type: "fixed", Value: 2000, Status: "active", StartDate: "2000-01-01", EndDate: "2999-01-01"})
	before := e.balance()
	if err := e.s.RechargeWithBalanceCoupon(ctx, e.cust.ID, e.day.ID, "C2"); err != nil {
		t.Fatal(err)
	}
	if p, note := lastPrice(e); p != 8000+800+800 || before-e.balance() != p || note == "" { // 8000 + 800 tax + 800 bills
		t.Fatalf("coupon price %d (%q) charged %d", p, note, before-e.balance())
	}
	// gateway: records exactly the amount it charged, still pays the installment
	claim := func(*db.Queries) (bool, error) { return true, nil }
	if err := e.s.RechargePaid(ctx, claim, e.cust.ID, e.day.ID, "Tripay - QRIS", "", 12345); err != nil {
		t.Fatal(err)
	}
	if p, _ := lastPrice(e); p != 12345 {
		t.Fatalf("gateway price %d", p)
	}
	if got := attrs(ctx, e.q, e.cust.ID)["Phone Bill"]; got != "300:0" {
		t.Fatalf("installment after gateway %q", got)
	}
}

// First Period activation is free with no bills and no tax; after that the Invoice attribute
// (IM1) is the base, with tax and bills on top. Vouchers carry no tax.
func TestFirstPeriodAndVoucherPrice(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	for k, v := range map[string]string{"enable_tax": "yes", "tax_rate": "10"} {
		e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v})
	}
	e.conn.Exec("UPDATE plans SET validity_unit = 'Period' WHERE id = ?", e.day.ID)
	setAttr(ctx, e.q, e.cust.ID, "Router Bill", "500")
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Cash - x", 0); err != nil {
		t.Fatal(err)
	}
	if p, _ := lastPrice(e); p != 0 {
		t.Fatalf("first period %d", p)
	}
	e.conn.Exec("UPDATE subscriptions SET status = 'expired'")
	setAttr(ctx, e.q, e.cust.ID, "Invoice", "4000")
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Cash - x", 0); err != nil {
		t.Fatal(err)
	}
	if p, _ := lastPrice(e); p != 4000+400+500 {
		t.Fatalf("second period %d", p)
	}
	e.conn.Exec("UPDATE plans SET validity_unit = 'Days' WHERE id = ?", e.day.ID)
	e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: "VV", PlanID: e.day.ID})
	if err := e.s.RedeemVoucher(ctx, "VV", e.cust.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := lastPrice(e); p != 10000+500 { // bills yes (as IM1), tax no
		t.Fatalf("voucher %d", p)
	}
}
