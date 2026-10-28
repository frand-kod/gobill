package web

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestDashboardWidgets(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	p := e.plan(t, "p1", "PPPoE", 25000)
	now := time.Now().Unix()
	if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: p.ID, RouterID: sql.NullInt64{Int64: e.rt, Valid: true},
		Type: "PPPoE", StartedAt: now, ExpiresAt: now + 3600, Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "admin", Action: "Recharge", Description: "widget-log-marker"}); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/admin", nil, e.c)
	body := w.Body.String()
	for _, want := range []string{
		`href="/admin/customers/` + strconv.FormatInt(e.cust.ID, 10) + `">u1<`, // expiring list links to the customer
		`widget-log-marker`, // activity log
	} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q (status %d)", want, w.Code)
		}
	}
	for _, gone := range []string{`data-chart="pie"`, `id="vouchers-title"`, `id="jobs-title"`} {
		if strings.Contains(body, gone) {
			t.Errorf("dashboard still has removed card %q", gone)
		}
	}
}

// The "Needs a look" strip lists the flagged items; manager-only items stay hidden from a Report role.
func TestDashboardNeedsALook(t *testing.T) {
	e := englishApp(t)
	now := time.Now().Unix()
	p := e.plan(t, "gold", "PPPoE", 25000)
	// ended 10 minutes ago and not renewed: recently expired, shown to every role
	if _, err := e.q.CreateSubscription(t.Context(), db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: p.ID,
		RouterID: sql.NullInt64{Int64: e.rt, Valid: true}, Type: "PPPoE", StartedAt: now - 7200, ExpiresAt: now - 600, Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	mkRouter(t, e.q, "core-down", 1, "api", sql.NullInt64{Int64: 0, Valid: true}, sql.NullInt64{})
	last := time.Now().In(e.srv.location()).Add(-time.Hour).Format(time.DateTime)
	setting(t, e, "expiry_last_run", last)

	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	for _, want := range []string{
		`id="attn-title"`,
		`href="#network-title"`, "1 router/NAS offline",
		`href="/admin/status"`, "Expiry job has not run since " + last,
		`href="#recent-title"`, "1 expired in the last 3 days, not renewed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin dashboard missing %q", want)
		}
	}

	report := roleLogin(t, e.srv, e.h, "Report")
	rep := do(e.h, "GET", "/admin", nil, report).Body.String()
	if !strings.Contains(rep, `id="attn-title"`) || !strings.Contains(rep, "not renewed") {
		t.Error("report role does not see the non-manager item")
	}
	for _, hidden := range []string{"router/NAS offline", "Expiry job has not run"} {
		if strings.Contains(rep, hidden) {
			t.Errorf("report role sees manager-only item %q", hidden)
		}
	}
}

// Nothing to flag: no strip at all.
func TestDashboardNeedsALookQuiet(t *testing.T) {
	e := englishApp(t)
	setting(t, e, "expiry_last_run", time.Now().In(e.srv.location()).Format(time.DateTime))
	if body := do(e.h, "GET", "/admin", nil, e.c).Body.String(); strings.Contains(body, `id="attn-title"`) {
		t.Fatal("attention strip shown with nothing to flag")
	}
}
