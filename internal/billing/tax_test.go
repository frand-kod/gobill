package billing

import (
	"context"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestWithTax(t *testing.T) {
	on := func(rate, custom string) map[string]string {
		return map[string]string{"enable_tax": "yes", "tax_rate": rate, "custom_tax_rate": custom}
	}
	for _, c := range []struct {
		st   map[string]string
		in   int64
		want int64
	}{
		{nil, 10000, 10000},
		{map[string]string{"enable_tax": "no", "tax_rate": "11"}, 10000, 10000},
		{on("11", ""), 10000, 11100},
		{on("custom", "2.5"), 10000, 10250},
		{on("11", ""), 15555, 17266}, // 1710.05 rounds to 1710
		{on("", ""), 10000, 10000},
		{on("abc", ""), 10000, 10000},
	} {
		if got := WithTax(c.st, c.in); got != c.want {
			t.Errorf("%v %d: got %d want %d", c.st, c.in, got, c.want)
		}
	}
}

// R7: with enable_tax the balance debit and the recorded price include the tax; vouchers and
// balance top-ups do not.
func TestTaxOnRecharge(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	for k, v := range map[string]string{"enable_tax": "yes", "tax_rate": "10"} {
		e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v})
	}
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 20000, ID: e.cust.ID})
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); err != nil {
		t.Fatal(err)
	}
	var price int64
	e.conn.QueryRow("SELECT price FROM transactions ORDER BY id DESC LIMIT 1").Scan(&price)
	if price != 11000 || e.balance() != 9000 {
		t.Fatalf("price %d balance %d", price, e.balance())
	}
	if pv, _ := e.s.Preview(ctx, e.cust.ID, e.day.ID); pv.Price != 11000 {
		t.Fatalf("preview %d", pv.Price)
	}
	// 9000 is above the plain price but below the taxed one
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); err != ErrInsufficientBalance {
		t.Fatalf("err %v", err)
	}
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Cash - x", 0); err != nil {
		t.Fatal(err)
	}
	e.conn.QueryRow("SELECT price FROM transactions ORDER BY id DESC LIMIT 1").Scan(&price)
	if price != 11000 {
		t.Fatalf("cash price %d", price)
	}
	if err := e.s.Recharge(ctx, e.cust.ID, e.topup.ID, "Cash - x", 0); err != nil {
		t.Fatal(err)
	}
	e.conn.QueryRow("SELECT price FROM transactions ORDER BY id DESC LIMIT 1").Scan(&price)
	if price != 50000 {
		t.Fatalf("topup price %d", price)
	}
}
