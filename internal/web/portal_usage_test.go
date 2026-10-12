package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// /portal/usage.json is the logged-in customer's own live card: another customer's session never shows.
func TestPortalUsageJSONIsolationAndRate(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	now := time.Now().Unix()
	dashPlan(t, e, "", 0, 0, now+86400)
	dashSess(t, e, "u1", "mine", "10.0.0.5", "CE:33:AA:BB:2A:AA", now-600, 0, 0, 0)
	other := otherCust(t, e)
	dashSess(t, e, other.Username, "theirs", "10.9.9.9", "DE:AD:BE:EF:00:01", now-600, 0, 5, 5)

	// first interim update, then a second one a minute later: 750 kB down in 60 s is 100 kbps
	if _, err := e.s.conn.ExecContext(t.Context(), `UPDATE radius_sessions SET updated_at = ? WHERE session_id = 'mine'`, now-60); err != nil {
		t.Fatal(err)
	}
	get := func() (pLive, string) {
		w := do(e.h, "GET", "/portal/usage.json", nil, c)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("usage.json: %d %s", w.Code, w.Header().Get("Content-Type"))
		}
		var v pLive
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v, w.Body.String()
	}
	get()
	if _, err := e.s.conn.ExecContext(t.Context(), `UPDATE radius_sessions SET updated_at = ?, output_octets = 750000 WHERE session_id = 'mine'`, now); err != nil {
		t.Fatal(err)
	}
	v, body := get()
	if !v.Online || v.IP != "10.0.0.5" || v.Device != "CE:33:••:••:2A:AA" {
		t.Fatalf("own session: %+v", v)
	}
	if v.Down != "100 kbps" || len(v.Bars) != 5 {
		t.Fatalf("rate or meter: down %q bars %v", v.Down, v.Bars)
	}
	if strings.Contains(body, "10.9.9.9") || strings.Contains(body, "DE:AD:BE:EF") || strings.Contains(body, "CE:33:AA:BB:2A:AA") {
		t.Fatal("other customer's session or the full MAC leaked into usage.json")
	}
	// no login: sent to the login page, not to the data
	if w := do(e.h, "GET", "/portal/usage.json", nil, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous usage.json: %d %s", w.Code, w.Header().Get("Location"))
	}
}

// The daily chart counts a session's octets on the day it was last updated, the last 7 days only.
func TestDailyUsageAggregation(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, loc).Unix()
	at := func(day, hour int) int64 { return time.Date(2026, 10, 12+day, hour, 0, 0, 0, loc).Unix() }
	rows := []db.ListRadiusUsageSinceRow{
		{UpdatedAt: at(0, 8), InputOctets: 1, OutputOctets: 2},
		{UpdatedAt: at(0, 9), InputOctets: 10, OutputOctets: 20},
		{UpdatedAt: at(-6, 23), InputOctets: 100, OutputOctets: 200},   // first day of the chart
		{UpdatedAt: at(-7, 23), InputOctets: 1000, OutputOctets: 2000}, // before the chart: dropped
	}
	if got := weekSince(now, loc); got != at(-6, 0) {
		t.Fatalf("weekSince = %d, want midnight six days back", got)
	}
	days := dailyUsage(rows, now, loc)
	if len(days) != 7 || !days[6].Today || days[6].Down != 22 || days[6].Up != 11 {
		t.Fatalf("today: %+v", days[6])
	}
	if days[0].Label != "6 Oct" || days[0].Down != 200 || days[0].Up != 100 {
		t.Fatalf("first day: %+v", days[0])
	}
	for i := 1; i < 6; i++ {
		if days[i].Down != 0 || days[i].Up != 0 {
			t.Fatalf("day %d should be empty: %+v", i, days[i])
		}
	}
	bars := chartBars(days)
	top := bars[0]
	if top.UpY != chartBase-chartH || top.DownY+top.DownH != chartBase || top.UpY+top.UpH != top.DownY {
		t.Fatalf("busiest bar does not fill the chart or does not stack: %+v", top)
	}
	if bars[6].DownH >= top.DownH || bars[6].Mid <= bars[6].X {
		t.Fatalf("today's bar: %+v", bars[6])
	}
}

// The dashboard: plan card with one buy or extend button, the live card, the usage card, the bottom tabs,
// and no session table.
func TestPortalDashboardCards(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	now := time.Now().Unix()
	dashPlan(t, e, "Both_Limit", 10, 1000, now+86400)
	body := portalGet(t, e, c, "/portal")
	for _, want := range []string{`id="c-plan"`, `aria-label="Validity used"`, `id="c-live"`, `x-data="portalLive()"`,
		`id="c-usage"`, `aria-label="Daily usage, last 7 days"`, `href="/portal/connection"`, `fixed inset-x-0 bottom-0`,
		`<script src="/static/usage.js?v=`} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard lacks %s", want)
		}
	}
	if n := strings.Count(body, "<span>Buy / Extend</span>"); n != 1 { // the "?" intro text also names it
		t.Fatalf("Buy / Extend buttons = %d, want 1", n)
	}
	if strings.Contains(body, "<th>Duration</th>") || strings.Contains(body, `id="riwayat"`) {
		t.Fatal("dashboard still has the connection session table")
	}
}

// The live card's script defines an Alpine component, so it must load without defer (Alpine is deferred).
func TestUsageScriptNotDeferred(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	body := portalGet(t, e, c, "/portal")
	if !strings.Contains(body, `<script src="/static/usage.js?v=`) || strings.Contains(body, `defer src="/static/usage.js`) {
		t.Fatal("usage.js must load without defer, before Alpine starts")
	}
}
