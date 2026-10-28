package radius

import (
	"context"
	"testing"
	"time"

	"layeh.com/radius/rfc2866"
)

func (e *env) usageDay(t *testing.T, user, day string) (in, out int64) {
	t.Helper()
	err := e.conn.QueryRow("SELECT COALESCE(SUM(input_octets),0), COALESCE(SUM(output_octets),0) FROM radius_usage_daily WHERE username = ? AND day = ?", user, day).Scan(&in, &out)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func (e *env) interim(t *testing.T, sid string, in, out int64) {
	t.Helper()
	err := e.s.Account(context.Background(), AcctRequest{Type: rfc2866.AcctStatusType_Value_InterimUpdate, NAS: "10.0.0.1", SessionID: sid, User: "alice", InOctets: in, OutOctets: out})
	if err != nil {
		t.Fatal(err)
	}
}

// A session's growth lands on the day it was reported, a repeated interim adds nothing, and a new
// session_id (NAS reboot) counts from its own zero.
func TestUsageDailyDeltas(t *testing.T) {
	e := setup(t)
	e.s.Loc = func() *time.Location { return time.UTC }
	e.now = time.Date(2026, 10, 12, 23, 55, 0, 0, time.UTC)
	e.interim(t, "s1", 100, 1000)
	e.now = time.Date(2026, 10, 13, 0, 5, 0, 0, time.UTC)
	e.interim(t, "s1", 130, 1500)
	if in, out := e.usageDay(t, "alice", "2026-10-12"); in != 100 || out != 1000 {
		t.Fatalf("day 1: %d/%d", in, out)
	}
	if in, out := e.usageDay(t, "alice", "2026-10-13"); in != 30 || out != 500 {
		t.Fatalf("day 2: %d/%d", in, out)
	}
	e.interim(t, "s1", 130, 1500) // identical retransmit
	e.interim(t, "s1", 50, 10)    // counters going back are ignored too
	if in, out := e.usageDay(t, "alice", "2026-10-13"); in != 30 || out != 500 {
		t.Fatalf("repeat changed day 2: %d/%d", in, out)
	}
	e.interim(t, "s2", 7, 9) // NAS rebooted: small counters of a new session
	if in, out := e.usageDay(t, "alice", "2026-10-13"); in != 37 || out != 509 {
		t.Fatalf("new session: %d/%d", in, out)
	}
}

// The app time zone decides the day.
func TestUsageDailyZone(t *testing.T) {
	e := setup(t)
	e.s.Loc = func() *time.Location { return time.FixedZone("WIB", 7*3600) }
	e.now = time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC) // 03:00 on the 13th in WIB
	e.interim(t, "s1", 5, 6)
	if in, out := e.usageDay(t, "alice", "2026-10-13"); in != 5 || out != 6 {
		t.Fatalf("%d/%d", in, out)
	}
}
