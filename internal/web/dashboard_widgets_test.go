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
		`href="#customers-expired-3"`, "1 expired in the last 3 days, not renewed",
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

// The Customers card: both tabs, the day filter, and rows carrying their calendar distance from today.
func TestDashboardCustomersCard(t *testing.T) {
	e := englishApp(t)
	ctx := t.Context()
	loc := e.srv.location()
	y, m, d := time.Now().In(loc).Date()
	noon := func(days int) int64 { return time.Date(y, m, d+days, 12, 0, 0, 0, loc).Unix() }
	p := e.plan(t, "p1", "PPPoE", 25000)
	// u1 ends in two days (expiring); u2 ended yesterday and was not renewed (expired)
	if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: p.ID, RouterID: sql.NullInt64{Int64: e.rt, Valid: true},
		Type: "PPPoE", StartedAt: noon(-30), ExpiresAt: noon(2), Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	u2 := otherCust(t, e)
	if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: u2.ID, PlanID: p.ID, RouterID: sql.NullInt64{Int64: e.rt, Valid: true},
		Type: "PPPoE", StartedAt: noon(-30), ExpiresAt: noon(-1), Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	for _, want := range []string{
		`id="customers"`, `x-data="gbCustomers()"`, `role="tablist"`,
		`role="tab" id="customers-tab-expiring"`, `role="tab" id="customers-tab-expired"`,
		`count('expiring')">1</span>`, `count('expired')">1</span>`, // tab counts before Alpine runs
		`data-days="2"`, `data-days="1"`, // ends in two days; ended yesterday
		`@click="f = 0"`, `@click="f = 3"`, `@click="f = 7"`,
		`href="/admin/customers/` + strconv.FormatInt(e.cust.ID, 10) + `">u1<`,
		`href="/admin/customers/` + strconv.FormatInt(u2.ID, 10) + `">u2<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("customers card missing %q", want)
		}
	}
	if strings.Contains(body, `id="expiring-title"`) || strings.Contains(body, `id="recent-title"`) {
		t.Error("old expiring and recent cards still rendered")
	}
}

// The Usage card: managers see the totals and the heaviest user; a Report role does not see it.
func TestDashboardUsageCard(t *testing.T) {
	e := englishApp(t)
	ctx := t.Context()
	loc := e.srv.location()
	if body := do(e.h, "GET", "/admin", nil, e.c).Body.String(); !strings.Contains(body, "No usage recorded yet") {
		t.Fatal("empty usage should say so")
	}
	now := time.Now().In(loc)
	for _, r := range []struct {
		user, day string
		in, out   int64
	}{
		{"u1", now.Format("2006-01-02"), 1 << 20, 3 << 20},
		{"u2", now.Format("2006-01-02"), 1 << 10, 2 << 10},
		{"u1", now.AddDate(0, 0, -20).Format("2006-01-02"), 1 << 20, 1 << 20}, // in the 30 day window only
	} {
		if _, err := e.s.conn.ExecContext(ctx, `INSERT INTO radius_usage_daily (username, day, input_octets, output_octets) VALUES (?, ?, ?, ?)`,
			r.user, r.day, r.in, r.out); err != nil {
			t.Fatal(err)
		}
	}
	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	week := humanBytes(4<<20 + 3<<10)
	month := humanBytes(6<<20 + 3<<10)
	for _, want := range []string{
		`id="usage-title"`, "RADIUS sessions only", ">" + week + "<", ">" + month + "<",
		`href="/admin/customers/` + strconv.FormatInt(e.cust.ID, 10) + `">u1<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("usage card missing %q", want)
		}
	}
	if strings.Contains(body, "No usage recorded yet") {
		t.Error("usage with rows still says none recorded")
	}
	report := roleLogin(t, e.srv, e.h, "Report")
	if rep := do(e.h, "GET", "/admin", nil, report).Body.String(); strings.Contains(rep, `id="usage-title"`) {
		t.Error("report role sees the usage card")
	}
}
