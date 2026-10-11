package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Two interim updates give the rate: the octets since the previous update over the seconds between them.
func TestLiveRateFromTwoSamples(t *testing.T) {
	var r liveRate
	r.add(liveSample{at: 100})
	if _, _, ok := r.bps(); ok {
		t.Fatal("one sample gives a rate")
	}
	r.add(liveSample{at: 100, in: 9, out: 9}) // the same interim update again is not a sample
	if _, _, ok := r.bps(); ok {
		t.Fatal("a repeated update counted as a sample")
	}
	// 60 s later: 7.5 MB sent to the user = 1 Mbps down, 375 kB received = 50 kbps up
	r.add(liveSample{at: 160, in: 375000, out: 7500000})
	down, up, ok := r.bps()
	if !ok || down != 1e6 || up != 5e4 {
		t.Fatalf("rate %v %v %v, want 1e6 5e4 true", down, up, ok)
	}
	// counters that go backwards (a reset) give no rate
	r.add(liveSample{at: 220})
	if _, _, ok := r.bps(); ok {
		t.Fatal("a counter reset gave a rate")
	}
}

// The monitor forgets sessions that are no longer open.
func TestLiveMonitorPrunesClosedSessions(t *testing.T) {
	var m liveMonitor
	m.refresh([]db.RadiusSession{{ID: 1, UpdatedAt: 100}, {ID: 2, UpdatedAt: 100}})
	m.refresh([]db.RadiusSession{{ID: 2, UpdatedAt: 100}})
	if len(m.rates) != 1 || m.rates[2] == nil {
		t.Fatalf("rates kept %d sessions, want only 2", len(m.rates))
	}
}

// Winbox thresholds: green below 50%, yellow from 50% to 75%, red above 75%. Segments are 20% each.
func TestMeterThresholds(t *testing.T) {
	for _, c := range []struct {
		pct  float64
		segs int
		tone string
	}{
		{0, 0, "ok"}, {49, 2, "ok"}, {50, 3, "warn"}, {75, 4, "warn"}, {76, 4, "err"}, {100, 5, "err"}, {130, 5, "err"},
	} {
		if m := meterOf(c.pct); m.Segs != c.segs || m.Tone != c.tone {
			t.Errorf("meterOf(%v) = %+v, want %d %s", c.pct, m, c.segs, c.tone)
		}
	}
	b := meterOf(49).bars()
	if len(b) != 5 || b[0] != "bg-meter-ok" || b[1] != "bg-meter-ok" || b[2] != "bg-line" {
		t.Fatalf("bars for 49%%: %v", b)
	}
}

// The meter follows the larger of the two shares; the text shows both directions.
func TestSpeedOfUsesLargerShare(t *testing.T) {
	plan := planSpeed{Name: "Gold", MaxDown: 10e6, MaxUp: 1e6, DownLabel: "10 Mbps"}
	text, m := speedOf(3.2e6, 0.9e6, true, plan)
	if text != "↓3,2 Mbps / 10 Mbps ↑900 kbps" {
		t.Fatalf("text %q", text)
	}
	if m.Tone != "err" || m.Segs != 5 { // upload is at 90% of its limit
		t.Fatalf("meter %+v, want red and full", m)
	}
	if text, m := speedOf(0, 0, false, plan); text != "—" || m.Segs != 0 {
		t.Fatalf("no samples: %q %+v", text, m)
	}
	if _, m := speedOf(5e6, 0, true, planSpeed{}); m.Segs != 0 {
		t.Fatal("a plan without a limit has no meter")
	}
}

// The first plan of a login (newest expiry) is the one shown, under both login names.
func TestPlanSpeedsNewestFirst(t *testing.T) {
	m := planSpeeds([]db.ListActivePlanRatesRow{
		{Username: "u1", PlanName: "Gold", RateDown: 10, RateDownUnit: "Mbps"},
		{Username: "u1", PlanName: "Old", RateDown: 2, RateDownUnit: "Mbps"},
		{Username: "c2", PppoeUsername: "pp2", PlanName: "Silver", RateDown: 512, RateDownUnit: "Kbps"},
	})
	if m["u1"].Name != "Gold" || m["u1"].MaxDown != 10e6 {
		t.Fatalf("u1 = %+v", m["u1"])
	}
	if m["pp2"].Name != "Silver" || m["pp2"].MaxDown != 512e3 || m["pp2"].DownLabel != "512 Kbps" {
		t.Fatalf("pp2 = %+v", m["pp2"])
	}
}

// The sparkline keeps the last 288 samples, oldest first, and no more.
func TestLiveSparklineBounded(t *testing.T) {
	var m liveMonitor
	for i := 0; i < liveSpark+50; i++ {
		m.sample(i)
	}
	got := m.series()
	if len(got) != liveSpark || got[0] != 50 || got[len(got)-1] != liveSpark+49 {
		t.Fatalf("series len %d first %d last %d", len(got), got[0], got[len(got)-1])
	}
}

// The live feed needs an admin login like the page, and its rate follows two interim updates.
func TestRadiusSessionsJSON(t *testing.T) {
	s, h, _, c := crudApp(t)
	now := time.Now().Unix()
	if _, err := s.conn.Exec(`INSERT INTO radius_sessions (session_id, username, nas_ip, framed_ip, mac, started_at, updated_at, input_octets, output_octets)
		VALUES ('s1', 'u1', '10.0.0.1', '10.20.0.5', 'AA:BB:CC:DD:EE:FF', ?, ?, 0, 0)`, now-120, now-60); err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "GET", "/admin/radius/sessions.json", nil, nil); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), `"rows"`) {
		t.Fatalf("feed without login: %d", rec.Code)
	}
	feed := func() (v liveView) {
		t.Helper()
		rec := do(h, "GET", "/admin/radius/sessions.json", nil, c)
		if rec.Code != http.StatusOK {
			t.Fatalf("feed status %d", rec.Code)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := feed()
	if v.Tiles.Online != 1 || len(v.Rows) != 1 || v.Rows[0].User != "u1" || v.Total != 1 {
		t.Fatalf("first feed: %+v", v)
	}
	if v.Rows[0].Speed != "—" { // one sample so far
		t.Fatalf("speed after one sample: %q", v.Rows[0].Speed)
	}
	// the next interim update, 60 s later, with 7.5 MB sent to the user: 1 Mbps down
	if _, err := s.conn.Exec(`UPDATE radius_sessions SET updated_at = ?, output_octets = ? WHERE session_id = 's1'`,
		now, 7500000); err != nil {
		t.Fatal(err)
	}
	v = feed()
	if want := "↓1,0 Mbps"; !strings.HasPrefix(v.Rows[0].Speed, want) {
		t.Fatalf("speed %q, want prefix %q", v.Rows[0].Speed, want)
	}
	if len(v.Spark) != 0 {
		t.Fatal("sparkline has samples before the sampler ran")
	}
	// the page itself renders the same rows and the tiles
	body := do(h, "GET", "/admin/radius/sessions", nil, c).Body.String()
	if !strings.Contains(body, `data-id="`) || !strings.Contains(body, "↓1,0 Mbps") {
		t.Fatal("page does not show the session and its speed")
	}
}

// The sampler adds the online count; the dashboard tile links to the live page.
func TestOnlineSampleAndDashboardLink(t *testing.T) {
	s, h, _, c := crudApp(t)
	if _, err := s.conn.Exec(`INSERT INTO radius_sessions (session_id, username, nas_ip, framed_ip, mac, started_at, updated_at)
		VALUES ('s9', 'u9', '10.0.0.1', '10.20.0.9', '', ?, ?)`, time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := s.SampleOnline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := s.live.series(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("sparkline %v, want [1]", got)
	}
	if body := do(h, "GET", "/admin", nil, c).Body.String(); !strings.Contains(body, `href="/admin/radius/sessions"`) {
		t.Fatal("dashboard has no link to the live sessions page")
	}
}
