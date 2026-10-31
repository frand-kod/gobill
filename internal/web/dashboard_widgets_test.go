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
	if err := e.q.UpdateCustomer(ctx, db.UpdateCustomerParams{Fullname: "U One", Phone: "081234567890", ServiceType: "PPPoE",
		AutoRenewal: 1, Status: "Active", ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/admin", nil, e.c)
	body := w.Body.String()
	// the username popover holds the full name and phone; the row's visible cells hold neither
	if i := strings.Index(body, `href="/admin/customers/`+strconv.FormatInt(e.cust.ID, 10)+`">u1<`); i >= 0 {
		start := strings.LastIndex(body[:i], "<tr ")
		row := body[start : i+strings.Index(body[i:], "</tr>")]
		pop := strings.Index(row, `class="gb-pop"`)
		if pop < 0 || !strings.Contains(row[pop:], "U One") || strings.Contains(row[:pop], "081234567890") {
			t.Errorf("customer row: popover or phone placement wrong: %s", row)
		}
	} else {
		t.Error("expiring row for u1 missing")
	}
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

// The admin bell counts the flagged items on every admin page; manager-only items stay hidden from a Report role.
func TestAdminBell(t *testing.T) {
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

	// a bell on a non-dashboard page: the router offline, the stalled job and the expired plan (3 items)
	body := do(e.h, "GET", "/admin/customers", nil, e.c).Body.String()
	for _, want := range []string{
		`id="bell-badge" data-count="3" class="`, "bell-err",
		`href="/admin/routers"`, "Router/NAS offline",
		`href="/admin/status"`, "Expiry job stalled",
		`href="/admin#customers-expired-3"`, "Expired in the last 3 days, not renewed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin bell missing %q", want)
		}
	}

	report := roleLogin(t, e.srv, e.h, "Report")
	rep := do(e.h, "GET", "/admin", nil, report).Body.String()
	if !strings.Contains(rep, `data-count="1"`) || strings.Contains(rep, "bell-err") || !strings.Contains(rep, "Expired in the last 3 days") {
		t.Error("report role does not see the non-manager item alone")
	}
	for _, hidden := range []string{"Router/NAS offline", "Expiry job stalled"} {
		if strings.Contains(rep, hidden) {
			t.Errorf("report role sees manager-only item %q", hidden)
		}
	}
}

// Nothing to flag: no badge, and the menu says so.
func TestAdminBellQuiet(t *testing.T) {
	e := englishApp(t)
	setting(t, e, "expiry_last_run", time.Now().In(e.srv.location()).Format(time.DateTime))
	body := do(e.h, "GET", "/admin/customers", nil, e.c).Body.String()
	if strings.Contains(body, `id="bell-badge"`) {
		t.Fatal("bell badge shown with nothing to flag")
	}
	if !strings.Contains(body, "Nothing to check") {
		t.Fatal("bell menu lacks the empty state")
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
	// the big number carries the total once; the 7 day and 30 day windows both render
	wn, wu := humanBytesParts(4<<20 + 3<<10)
	mn, mu := humanBytesParts(6<<20 + 3<<10)
	for _, want := range []string{
		`id="usage-title"`, "RADIUS sessions only",
		`class="stat-num">` + wn + `<span class="ms-1 text-sm font-medium text-ink-2">` + wu + `</span>`,
		`class="stat-num">` + mn + `<span class="ms-1 text-sm font-medium text-ink-2">` + mu + `</span>`,
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
