package billing

import (
	"context"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
)

func enableIsolir(t *testing.T, e *env, days string) {
	t.Helper()
	ctx := context.Background()
	for k, v := range map[string]string{"isolir_enabled": "yes", "isolir_days": days} {
		if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
}

// activeOf returns the single active subscription, failing when there is not exactly one.
func activeOf(t *testing.T, e *env) db.Subscription {
	t.Helper()
	var out []db.Subscription
	for _, s := range e.subs(t) {
		if s.Status == "active" {
			out = append(out, s)
		}
	}
	if len(out) != 1 {
		t.Fatalf("want 1 active subscription, got %+v", e.subs(t))
	}
	return out[0]
}

func TestIsolirExpiryMovesToIsolirPlan(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2) // the day plan ended one day after the recharge
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	iso, err := e.q.GetSetting(ctx, "isolir_plan_id")
	if err != nil || iso == "" {
		t.Fatalf("isolir plan not stored: %v", err)
	}
	s := activeOf(t, e)
	plan, _ := e.q.GetPlan(ctx, s.PlanID)
	if plan.Name != isolir.PlanName || plan.Device != "Radius" || plan.RouterID.Valid {
		t.Fatalf("active plan %+v, want the Radius isolir plan", plan)
	}
	if want := e.now.AddDate(0, 0, 5).Unix(); s.ExpiresAt != want || s.StartedAt != e.now.Unix() {
		t.Fatalf("isolation window %d..%d, want %d..%d", s.StartedAt, s.ExpiresAt, e.now.Unix(), want)
	}
}

func TestIsolirEndsAfterDaysThenExpired(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	e.now = e.now.AddDate(0, 0, 6) // past the isolation
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	for _, s := range e.subs(t) {
		if s.Status == "active" {
			t.Fatalf("still active after isolir_days: %+v", s)
		}
	}
	if n := len(e.subs(t)); n != 2 {
		t.Fatalf("want 2 subscriptions (expired plan, expired isolation), got %d", n)
	}
}

func TestIsolirZeroDaysUntilPaid(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "0")
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	e.now = e.now.AddDate(0, 0, 400)
	e.s.ExpireDue(ctx)
	if s := activeOf(t, e); s.ExpiresAt != isolir.Forever.Unix() {
		t.Fatalf("expiry %d, want forever", s.ExpiresAt)
	}
}

func TestIsolirExplicitExpiredPlanWins(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	if _, err := e.conn.Exec("UPDATE plans SET expired_plan_id = ? WHERE id = ?", e.day2.ID, e.day.ID); err != nil {
		t.Fatal(err)
	}
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	for _, s := range e.subs(t) {
		if s.Status == "active" {
			t.Fatalf("explicit expired plan must not isolate: %+v", s)
		}
	}
}

func TestIsolirOffKeepsExpiry(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	for _, s := range e.subs(t) {
		if s.Status == "active" {
			t.Fatalf("isolir off must expire: %+v", s)
		}
	}
}

func TestRechargeEndsIsolation(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day2.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	s := activeOf(t, e)
	if s.PlanID != e.day2.ID {
		t.Fatalf("active plan %d after recharge, want %d", s.PlanID, e.day2.ID)
	}
}
