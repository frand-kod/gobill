package billing

import (
	"context"
	"testing"
	"time"

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

// radiusPlans puts the test plans on the Radius device: only those can be isolated.
func radiusPlans(t *testing.T, e *env) {
	t.Helper()
	if _, err := e.conn.Exec(`UPDATE plans SET device = 'Radius' WHERE type != 'Balance'`); err != nil {
		t.Fatal(err)
	}
}

// isolationRows returns every isolation, running or ended, in the order they started.
func isolationRows(t *testing.T, e *env) []db.Isolation {
	t.Helper()
	rows, err := e.conn.Query(`SELECT id, customer_id, started_at, ends_at, ended_at, end_reason FROM isolations ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []db.Isolation
	for rows.Next() {
		var i db.Isolation
		if err := rows.Scan(&i.ID, &i.CustomerID, &i.StartedAt, &i.EndsAt, &i.EndedAt, &i.EndReason); err != nil {
			t.Fatal(err)
		}
		out = append(out, i)
	}
	return out
}

// runningIsolation returns the single running isolation, failing when there is not exactly one.
func runningIsolation(t *testing.T, e *env) db.Isolation {
	t.Helper()
	running, err := e.q.ListRunningIsolations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 {
		t.Fatalf("want 1 running isolation, got %+v", running)
	}
	return running[0]
}

// notIsolated fails when a customer is isolated now.
func notIsolated(t *testing.T, e *env) {
	t.Helper()
	running, err := e.q.ListRunningIsolations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 0 {
		t.Fatalf("must not be isolated: %+v", running)
	}
}

// noIsolation fails when a customer is isolated now, and when a subscription is active (no plan bought).
func noIsolation(t *testing.T, e *env) {
	t.Helper()
	notIsolated(t, e)
	for _, s := range e.subs(t) {
		if s.Status == "active" {
			t.Fatalf("must not be isolated, subscription still active: %+v", s)
		}
	}
}

func TestIsolirExpiryMovesToIsolirPlan(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	radiusPlans(t, e)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2) // the day plan ended one day after the recharge
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	iso := runningIsolation(t, e)
	if want := e.now.AddDate(0, 0, 5).Unix(); iso.EndsAt != want || iso.StartedAt != e.now.Unix() {
		t.Fatalf("isolation window %d..%d, want %d..%d", iso.StartedAt, iso.EndsAt, e.now.Unix(), want)
	}
	for _, s := range e.subs(t) {
		if s.Status == "active" {
			t.Fatalf("isolation must not be a subscription: %+v", s)
		}
	}
}

func TestIsolirEndsAfterDaysThenExpired(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	radiusPlans(t, e)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	e.now = e.now.AddDate(0, 0, 6) // past the isolation
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	noIsolation(t, e)
	rows := isolationRows(t, e)
	if len(rows) != 1 || rows[0].EndReason != "expired" || rows[0].EndedAt != e.now.Unix() {
		t.Fatalf("want one isolation ended as expired now, got %+v", rows)
	}
	if n := len(e.subs(t)); n != 1 {
		t.Fatalf("want 1 subscription (the expired plan) and no isolir subscription, got %d", n)
	}
}

func TestIsolirZeroDaysUntilPaid(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "0")
	radiusPlans(t, e)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	e.now = e.now.AddDate(0, 0, 400)
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	if iso := runningIsolation(t, e); iso.EndsAt != isolir.Forever.Unix() {
		t.Fatalf("end %d, want forever", iso.EndsAt)
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
	noIsolation(t, e)
}

func TestIsolirOffKeepsExpiry(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	noIsolation(t, e)
}

func TestRechargeEndsIsolation(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	radiusPlans(t, e)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day2.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	notIsolated(t, e)
	rows := isolationRows(t, e)
	if len(rows) != 1 || rows[0].EndReason != "paid" {
		t.Fatalf("want the isolation ended as paid, got %+v", rows)
	}
	s := activeOf(t, e)
	if s.PlanID != e.day2.ID {
		t.Fatalf("active plan %d after recharge, want %d", s.PlanID, e.day2.ID)
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

// Switching isolation off ends the running isolation, so the customer is not left isolated.
func TestEndIsolationsWhenSwitchedOff(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	radiusPlans(t, e)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	runningIsolation(t, e) // there is an isolation to end
	if err := e.s.EndIsolations(ctx); err != nil {
		t.Fatal(err)
	}
	noIsolation(t, e)
	if rows := isolationRows(t, e); len(rows) != 1 || rows[0].EndReason != "disabled" {
		t.Fatalf("want the isolation ended as disabled, got %+v", rows)
	}
}

func TestIsolirSkipsNonRadiusPlan(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	for _, dev := range []string{"MikrotikPppoe", "MikrotikHotspot", "Dummy", ""} {
		if _, err := e.conn.Exec(`UPDATE plans SET device = ? WHERE id = ?`, dev, e.day.ID); err != nil {
			t.Fatal(err)
		}
		e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
		e.now = e.now.AddDate(0, 0, 2)
		e.s.ExpireDue(ctx)
		noIsolation(t, e)
	}
}

func TestIsolirSkipsInactiveCustomer(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	radiusPlans(t, e)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	if _, err := e.conn.Exec(`UPDATE customers SET status = 'Banned' WHERE id = ?`, e.cust.ID); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	noIsolation(t, e)
}

// Ending an isolation is not an expiry the customer is told about: no early notice, no WhatsApp, no webhook.
func TestIsolirExpirySendsNoNotice(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	enableIsolir(t, e, "5")
	radiusPlans(t, e)
	e.conn.Exec(`UPDATE customers SET phone = "08123456789"`)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2) // the plan ends, isolation starts
	e.s.ExpireDue(ctx)
	iso := runningIsolation(t, e)
	got := withNotify(t, e)
	e.now = time.Unix(iso.EndsAt-5*60, 0) // inside the early notice window
	e.s.ExpireDue(ctx)
	e.now = time.Unix(iso.EndsAt+60, 0) // isolation over
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	noIsolation(t, e)
	if reqs := drain(got); len(reqs) != 0 {
		t.Fatalf("isolir expiry notified: %v", reqs)
	}
}
