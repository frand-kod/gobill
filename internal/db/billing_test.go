package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestBillingSchema(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := New(conn)

	bw, err := q.CreateBandwidth(ctx, CreateBandwidthParams{Name: "5M", RateDown: 5, RateDownUnit: "Mbps", RateUp: 5, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := q.CreateRouter(ctx, CreateRouterParams{Name: "r1", Host: "10.0.0.1", Port: 8728, Username: "admin", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := q.CreatePlan(ctx, CreatePlanParams{
		Name: "p1", Type: "Hotspot", Billing: "prepaid", Price: 10000, Validity: 1, ValidityUnit: "Days",
		BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}, Enabled: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	cust, err := q.CreateCustomer(ctx, CreateCustomerParams{Username: "u1", PasswordHash: "h", Fullname: "U One", ServiceType: "Hotspot", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := q.CreateSubscription(ctx, CreateSubscriptionParams{
		CustomerID: cust.ID, PlanID: plan.ID, RouterID: rt.ID, Type: "Hotspot", StartedAt: 100, ExpiresAt: 200,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Expiry job flow, idempotent.
	if got, _ := q.ListExpiredActiveSubscriptions(ctx, 150); len(got) != 0 {
		t.Fatal("not yet expired")
	}
	if got, _ := q.ListExpiredActiveSubscriptions(ctx, 200); len(got) != 1 {
		t.Fatal("should be expired")
	}
	for i, want := range []int64{1, 0} {
		if n, err := q.ExpireSubscription(ctx, sub.ID); err != nil || n != want {
			t.Fatalf("expire #%d: n=%d err=%v", i, n, err)
		}
	}

	// One active subscription per customer+router+type; allowed again once expired.
	dup := CreateSubscriptionParams{CustomerID: cust.ID, PlanID: plan.ID, RouterID: rt.ID, Type: "Hotspot", StartedAt: 300, ExpiresAt: 400}
	if _, err := q.CreateSubscription(ctx, dup); err != nil {
		t.Fatalf("new sub after expiry: %v", err)
	}
	if _, err := q.CreateSubscription(ctx, dup); err == nil {
		t.Fatal("second active subscription accepted")
	}

	// Voucher claimed once only.
	v, err := q.CreateVoucher(ctx, CreateVoucherParams{Code: "ABC", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{1, 0} {
		n, err := q.UseVoucher(ctx, UseVoucherParams{UsedBy: sql.NullInt64{Int64: cust.ID, Valid: true}, ID: v.ID})
		if err != nil || n != want {
			t.Fatalf("use voucher #%d: n=%d err=%v", i, n, err)
		}
	}

	// Balance adjust, overdraw rejected.
	if b, err := q.AdjustBalance(ctx, AdjustBalanceParams{Delta: 500, ID: cust.ID}); err != nil || b != 500 {
		t.Fatalf("balance %d %v", b, err)
	}
	if _, err := q.AdjustBalance(ctx, AdjustBalanceParams{Delta: -501, ID: cust.ID}); err == nil {
		t.Fatal("overdraw accepted")
	}

	// Search.
	if got, _ := q.SearchCustomers(ctx, SearchCustomersParams{Q: "One", PageLimit: 10}); len(got) != 1 {
		t.Fatal("search")
	}

	// FK and CHECK enforcement.
	bad := []struct{ name, stmt string }{
		{"fk subscription customer", `INSERT INTO subscriptions (customer_id, plan_id, router_id, type, started_at, expires_at) VALUES (999, 1, 1, 'Hotspot', 1, 2)`},
		{"fk plan router", `INSERT INTO plans (name, type, price, validity, validity_unit, bandwidth_id, router_id) VALUES ('x', 'Hotspot', 1, 1, 'Days', 1, 999)`},
		{"fk delete referenced plan", `DELETE FROM plans`},
		{"check plan type", `INSERT INTO plans (name, type, price, validity, validity_unit) VALUES ('y', 'Foo', 1, 1, 'Days')`},
		{"check validity unit", `INSERT INTO plans (name, type, price, validity, validity_unit) VALUES ('y', 'Balance', 1, 1, 'Years')`},
		{"check hotspot needs router", `INSERT INTO plans (name, type, price, validity, validity_unit) VALUES ('y', 'Hotspot', 1, 1, 'Days')`},
		{"check sub status", `UPDATE subscriptions SET status = 'weird'`},
		{"check customer status", `UPDATE customers SET status = 'weird'`},
		{"check negative price", `UPDATE plans SET price = -1`},
		{"unique username", `INSERT INTO customers (username, password_hash, fullname) VALUES ('u1', 'h', 'dup')`},
		{"check device", `UPDATE plans SET device = 'Nope'`},
		{"check limit_type", `UPDATE plans SET limit_type = 'Nope'`},
		{"check plan billing_day", `UPDATE plans SET billing_day = 32`},
		{"check customer billing_day", `UPDATE customers SET billing_day = 0`},
		{"fk expired_plan", `UPDATE plans SET expired_plan_id = 999`},
		{"voucher status/used_at", `UPDATE vouchers SET status = 'unused'`},
	}
	for _, c := range bad {
		if _, err := conn.Exec(c.stmt); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}
