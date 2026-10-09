package radius

import (
	"fmt"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

// Time_Limit is the total online time since activation (Max-All-Session), not per login.
func TestTimeLimitCumulative(t *testing.T) {
	e := setup(t)
	now := e.now.Unix()
	if _, err := e.conn.Exec(fmt.Sprintf("UPDATE plans SET limited=1, limit_type='Time_Limit', time_limit=30, time_unit='Mins'; UPDATE subscriptions SET started_at=%d", now-5000)); err != nil {
		t.Fatal(err)
	}
	r := e.auth(t, pap("alice", "pw"))
	if got := rfc2865.SessionTimeout_Get(r); r.Code != radius.CodeAccessAccept || got != 1800 {
		t.Fatalf("fresh: %v timeout %d", r.Code, got)
	}
	sess := func(id string, from, to int64) {
		if _, err := e.conn.Exec("INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at) VALUES (?, 'alice', '10.0.0.1', ?, ?, ?)", id, from, to, to); err != nil {
			t.Fatal(err)
		}
	}
	sess("a", now-3000, now-1800) // 1200s used
	r = e.auth(t, pap("alice", "pw"))
	if got := rfc2865.SessionTimeout_Get(r); r.Code != radius.CodeAccessAccept || got != 600 {
		t.Fatalf("after 1200s: %v timeout %d", r.Code, got)
	}
	sess("b", now-1700, now-1000) // 700s more: 1900 > 1800
	if r = e.auth(t, pap("alice", "pw")); r.Code != radius.CodeAccessReject {
		t.Fatalf("exhausted must reject, got %v", r.Code)
	}
}
