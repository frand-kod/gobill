package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// usageEnv fixes the clock at 2026-03-15 12:00 and seeds usage: u1 today and 20 days ago, u2 45 days ago,
// u3 the day after today. A closed session with huge octets proves the sessions table is not read.
func usageEnv(t *testing.T) (*billEnv, time.Time) {
	t.Helper()
	e := englishApp(t)
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, e.srv.location())
	e.srv.clock = func() time.Time { return now }
	day := func(back int) string { return now.AddDate(0, 0, -back).Format("2006-01-02") }
	for _, r := range []struct {
		user, day string
		in, out   int64
	}{
		{"u1", day(0), 1 << 20, 3 << 20}, {"u2", day(0), 1 << 10, 2 << 10},
		{"u1", day(20), 1 << 20, 1 << 20}, {"u2", day(45), 5 << 20, 5 << 20}, {"u3", day(-1), 9 << 20, 9 << 20},
	} {
		if _, err := e.s.conn.Exec(`INSERT INTO radius_usage_daily (username, day, input_octets, output_octets) VALUES (?, ?, ?, ?)`,
			r.user, r.day, r.in, r.out); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.s.conn.Exec(`INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, input_octets, output_octets)
		VALUES ('s1', 'u1', '10.0.0.1', ?, ?, ?, ?)`, now.Unix(), now.Unix(), 99<<30, 99<<30); err != nil {
		t.Fatal(err)
	}
	return e, now
}

func statNum(w usageWin) string {
	return `class="stat-num">` + w.TotalNum + `<span class="ms-1 text-sm font-medium text-ink-2">` + w.TotalUnit + `</span>`
}

// The report usage over 30 days equals the dashboard 30 day card, ignores sessions, and stops at from and to.
func TestReportUsageMatchesDashboard(t *testing.T) {
	e, now := usageEnv(t)
	d, err := e.srv.dashboardData(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	month := d.Usage.Month
	if month.Empty || month.Total != humanBytes(6<<20+3<<10) {
		t.Fatalf("dashboard 30 day total %q", month.Total)
	}
	to := now.Format("2006-01-02")
	body := do(e.h, "GET", "/admin/reports/period?from="+now.AddDate(0, 0, -29).Format("2006-01-02")+"&to="+to, nil, e.c).Body.String()
	for _, want := range []string{`id="usage-title"`, "RADIUS sessions only", statNum(month), "/admin/reports/usage.csv?"} {
		if !strings.Contains(body, want) {
			t.Errorf("report missing %q", want)
		}
	}
	// only today: u1 4 MiB + u2 3 KiB; the 20 day old, 45 day old and tomorrow rows are out
	day := do(e.h, "GET", "/admin/reports/period?from="+to+"&to="+to, nil, e.c).Body.String()
	n, u := humanBytesParts(4<<20 + 3<<10)
	if want := `class="stat-num">` + n + `<span class="ms-1 text-sm font-medium text-ink-2">` + u + `</span>`; !strings.Contains(day, want) {
		t.Errorf("one day range: want %q", want)
	}
	// a range with no usage says so; a backwards range too
	for _, q := range []string{"from=2020-01-01&to=2020-01-31", "from=2026-03-15&to=2026-03-01"} {
		if b := do(e.h, "GET", "/admin/reports/period?"+q, nil, e.c).Body.String(); !strings.Contains(b, "No usage recorded yet") {
			t.Errorf("%s: empty usage not shown", q)
		}
	}
}

// With no from and to the Period Report starts at the dashboard income period (reset_day) and ends today.
func TestReportDefaultRangeIsDashboardPeriod(t *testing.T) {
	e, now := usageEnv(t)
	loc := e.srv.location()
	for _, rd := range []string{"", "10", "20"} { // calendar month; cycle began this month; cycle began last month
		setting(t, e, "reset_day", rd)
		for i, at := range []time.Time{time.Date(2026, 2, 25, 9, 0, 0, 0, loc), time.Date(2026, 3, 3, 9, 0, 0, 0, loc),
			time.Date(2026, 3, 12, 9, 0, 0, 0, loc), now} {
			if _, err := e.s.conn.Exec(`INSERT OR IGNORE INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
				VALUES (?, 'u', 'p', 'r', 'PPPoE', ?, 'Cash', ?, 0, 0)`, "D-"+itoa(int64(i)), 100*(i+1), at.Unix()); err != nil {
				t.Fatal(err)
			}
		}
		dash, err := e.srv.dashboardData(t.Context(), now)
		if err != nil {
			t.Fatal(err)
		}
		d, _, err := e.srv.reportLoad(httptest.NewRequest("GET", "/admin/reports/period", nil).WithContext(t.Context()), true)
		if err != nil {
			t.Fatal(err)
		}
		if d.Sum != dash.IncomeMonth || d.Sum == 0 || d.To != "2026-03-15" {
			t.Errorf("reset_day %q: report %d to %s, dashboard %d", rd, d.Sum, d.To, dash.IncomeMonth)
		}
		wantFrom := map[string]string{"": "2026-03-01", "10": "2026-03-10", "20": "2026-02-20"}[rd]
		if d.From != wantFrom {
			t.Errorf("reset_day %q: from %s, want %s", rd, d.From, wantFrom)
		}
	}
}

// The usage CSV has one row per user per day in the range; the Report role gets neither CSV nor section.
func TestReportUsageCSVAndRoles(t *testing.T) {
	e, now := usageEnv(t)
	to := now.Format("2006-01-02")
	q := "?from=" + now.AddDate(0, 0, -29).Format("2006-01-02") + "&to=" + to
	w := do(e.h, "GET", "/admin/reports/usage.csv"+q, nil, e.c)
	want := "Day,Username,Download bytes,Upload bytes,Total bytes\n" +
		now.AddDate(0, 0, -20).Format("2006-01-02") + ",u1,1048576,1048576,2097152\n" +
		to + ",u1,3145728,1048576,4194304\n" +
		to + ",u2,2048,1024,3072\n"
	if w.Code != 200 || w.Body.String() != want || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("csv %d %q, want %q", w.Code, w.Body.String(), want)
	}
	report := roleLogin(t, e.srv, e.h, "Report")
	if w := do(e.h, "GET", "/admin/reports/usage.csv"+q, nil, report); w.Code == 200 || strings.Contains(w.Body.String(), "u1") {
		t.Errorf("report role got the usage csv: %d", w.Code)
	}
	b := do(e.h, "GET", "/admin/reports/period"+q, nil, report)
	if b.Code != 200 || strings.Contains(b.Body.String(), `id="usage-title"`) || strings.Contains(b.Body.String(), "usage.csv") {
		t.Errorf("report role sees usage: %d", b.Code)
	}
}
