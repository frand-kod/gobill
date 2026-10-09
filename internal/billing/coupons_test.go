package billing

import (
	"errors"
	"sync"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestDiscount(t *testing.T) {
	base := db.Coupon{Type: "fixed", Value: 2000, Status: "active", StartDate: "2025-01-01", EndDate: "2025-01-31"}
	with := func(f func(*db.Coupon)) db.Coupon { c := base; f(&c); return c }
	for _, c := range []struct {
		name  string
		c     db.Coupon
		price int64
		day   string
		want  int64
		err   error
	}{
		{"fixed", base, 10000, "2025-01-10", 8000, nil},
		{"first day", base, 10000, "2025-01-01", 8000, nil},
		{"last day", base, 10000, "2025-01-31", 8000, nil},
		{"percent", with(func(c *db.Coupon) { c.Type, c.Value = "percent", 15 }), 10000, "2025-01-10", 8500, nil},
		{"percent rounds half up", with(func(c *db.Coupon) { c.Type, c.Value = "percent", 15 }), 9999, "2025-01-10", 9999 - 1500, nil}, // 1499.85 -> 1500
		{"percent capped", with(func(c *db.Coupon) { c.Type, c.Value, c.MaxDiscount = "percent", 50, 1000 }), 10000, "2025-01-10", 9000, nil},
		{"inactive", with(func(c *db.Coupon) { c.Status = "inactive" }), 10000, "2025-01-10", 0, ErrCouponInactive},
		{"before start", base, 10000, "2024-12-31", 0, ErrCouponDate},
		{"after end", base, 10000, "2025-02-01", 0, ErrCouponDate},
		{"usage reached", with(func(c *db.Coupon) { c.MaxUsage, c.Used = 3, 3 }), 10000, "2025-01-10", 0, ErrCouponUsed},
		{"usage left", with(func(c *db.Coupon) { c.MaxUsage, c.Used = 3, 2 }), 10000, "2025-01-10", 8000, nil},
		{"min order", with(func(c *db.Coupon) { c.MinOrder = 10001 }), 10000, "2025-01-10", 0, ErrCouponMin},
		{"discount >= price", base, 2000, "2025-01-10", 0, ErrCouponTooBig},
	} {
		got, err := Discount(c.c, c.price, c.day)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("%s: got %d, %v; want %d, %v", c.name, got, err, c.want, c.err)
		}
	}
}

func TestCouponRecharge(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	cp, err := e.q.CreateCoupon(ctx, db.CreateCouponParams{Code: "SAVE20", Type: "percent", Value: 20, MaxUsage: 1, Status: "active", StartDate: "2025-01-01", EndDate: "2025-01-31"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "U2", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{e.cust.ID, other.ID} {
		if _, err := e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 10000, ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	// two buyers race for the single use: exactly one wins
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []int64{e.cust.ID, other.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.s.RechargeWithBalanceCoupon(ctx, id, e.day.ID, "SAVE20")
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrCouponUsed) {
			t.Fatalf("loser error: %v", err)
		}
	}
	got, _ := e.q.GetCoupon(ctx, cp.ID)
	if ok != 1 || got.Used != 1 {
		t.Fatalf("winners %d, used %d", ok, got.Used)
	}
	// the winner paid the discounted price, the note names the coupon, the loser kept its balance
	trx, _ := e.q.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10})
	if len(trx) != 1 || trx[0].Price != 8000 || trx[0].Note != "Coupon SAVE20" {
		t.Fatalf("trx %+v", trx)
	}
	var total int64
	for _, id := range []int64{e.cust.ID, other.ID} {
		c, _ := e.q.GetCustomer(ctx, id)
		total += c.Balance
	}
	if total != 12000 {
		t.Fatalf("balances sum %d, want 12000", total)
	}
	if err := e.s.RechargeWithBalanceCoupon(ctx, e.cust.ID, e.day.ID, "NOPE"); !errors.Is(err, ErrCouponNotFound) {
		t.Fatalf("unknown code: %v", err)
	}
}
