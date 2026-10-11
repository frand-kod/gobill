package billing

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// Admin-audience and Rp 0 plans: a customer can never take them, an admin can still give them.
func TestAdminAndZeroPlansOnlyGivenByAdmin(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 50000, ID: e.cust.ID})
	mk := func(name string, price int64, aud string) db.Plan {
		p, err := e.q.CreatePlan(ctx, db.CreatePlanParams{Name: name, Type: "PPPoE", Billing: "prepaid", Price: price, Validity: 1,
			ValidityUnit: "Days", Enabled: 1, Audience: aud, BandwidthID: e.day.BandwidthID, RouterID: e.day.RouterID})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	staff, zero := mk("staff", 10000, "admin"), mk("tech", 0, "public")

	for _, p := range []db.Plan{staff, zero} {
		if err := e.s.RechargeWithBalance(ctx, e.cust.ID, p.ID, 0); !errors.Is(err, ErrAdminOnlyPlan) {
			t.Fatalf("balance %s: %v", p.Name, err)
		}
		if err := e.s.RechargeWithBalanceCoupon(ctx, e.cust.ID, p.ID, "NOPE"); !errors.Is(err, ErrAdminOnlyPlan) {
			t.Fatalf("balance coupon %s: %v", p.Name, err)
		}
		if err := e.s.SendPlan(ctx, e.cust.ID, "u2", p.ID); !errors.Is(err, ErrAdminOnlyPlan) {
			t.Fatalf("friend %s: %v", p.Name, err)
		}
	}
	if e.balance() != 50000 || len(e.subs(t)) != 0 {
		t.Fatalf("refused purchase changed state: balance %d subs %d", e.balance(), len(e.subs(t)))
	}

	// the admin gives the admin plan: no balance debit, subscription active
	if err := e.s.Recharge(ctx, e.cust.ID, staff.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if s := e.sub(t); s.PlanID != staff.ID || e.balance() != 50000 {
		t.Fatalf("admin recharge: plan %d balance %d", s.PlanID, e.balance())
	}

	// the customer cannot extend the expired admin plan
	set(t, e, "extend_expired", "yes", "extend_days", "3")
	subID := e.sub(t).ID
	e.now = e.now.AddDate(0, 0, 5)
	if _, err := e.conn.Exec("UPDATE subscriptions SET status = 'expired'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.ExtendExpired(ctx, e.cust.ID, subID); !errors.Is(err, ErrAdminOnlyPlan) {
		t.Fatalf("extend admin plan: %v", err)
	}
}

// An admin-audience plan keeps renewing itself when auto-renew is on: priced from the balance,
// Rp 0 for free. A public Rp 0 plan does not (covered by the sellable guard).
func TestAutoRenewKeepsAdminPlan(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	for _, price := range []int64{10000, 0} {
		staff, err := e.q.CreatePlan(ctx, db.CreatePlanParams{Name: fmt.Sprintf("staff%d", price), Type: "PPPoE", Billing: "prepaid", Price: price, Validity: 1,
			ValidityUnit: "Days", Enabled: 1, Audience: "admin", BandwidthID: e.day.BandwidthID, RouterID: e.day.RouterID})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.s.Recharge(ctx, e.cust.ID, staff.ID, "Admin - Cash", 0); err != nil {
			t.Fatal(err)
		}
		before := e.balance()
		if price > 0 {
			e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 50000, ID: e.cust.ID})
			before += 50000
		}
		e.now = e.now.AddDate(0, 0, 2)
		if err := e.s.ExpireDue(ctx); err != nil {
			t.Fatal(err)
		}
		active := false
		for _, x := range e.subs(t) {
			if x.Status == "active" && x.PlanID == staff.ID {
				active = true
			}
		}
		if !active {
			t.Fatalf("admin plan (Rp %d) not renewed", price)
		}
		if got := e.balance(); got != before-price {
			t.Fatalf("Rp %d plan: balance %d, want %d", price, got, before-price)
		}
	}
}
