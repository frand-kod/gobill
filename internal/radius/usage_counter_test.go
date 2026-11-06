package radius

import (
	"context"
	"testing"

	"layeh.com/radius/rfc2866"
)

func (e *env) used(t *testing.T) (in, out, secs int64) {
	t.Helper()
	if err := e.conn.QueryRow("SELECT used_in_octets, used_out_octets, used_seconds FROM subscriptions").Scan(&in, &out, &secs); err != nil {
		t.Fatal(err)
	}
	return
}

func (e *env) acct(t *testing.T, typ rfc2866.AcctStatusType, user, sid string, sessTime, in, out int64) {
	t.Helper()
	if err := e.s.Account(context.Background(), AcctRequest{Type: typ, NAS: "10.0.0.1", SessionID: sid, User: user,
		SessionTime: sessTime, InOctets: in, OutOctets: out}); err != nil {
		t.Fatal(err)
	}
}

// Accounting adds each session's growth to the subscription. Repeats and counters going back add
// nothing, and pruning the session log leaves the counters alone.
func TestUsageCounters(t *testing.T) {
	e := setup(t) // the subscription started 100 s ago
	e.acct(t, rfc2866.AcctStatusType_Value_Start, "alice", "s1", 0, 0, 0)
	e.now = e.now.Add(60e9)
	e.acct(t, rfc2866.AcctStatusType_Value_InterimUpdate, "alice", "s1", 60, 100, 1000)
	e.acct(t, rfc2866.AcctStatusType_Value_InterimUpdate, "alice", "s1", 60, 100, 1000) // retransmit
	e.acct(t, rfc2866.AcctStatusType_Value_InterimUpdate, "alice", "s1", 60, 50, 10)    // going back
	e.now = e.now.Add(30e9)
	e.acct(t, rfc2866.AcctStatusType_Value_Stop, "alice", "s1", 90, 130, 1500)
	e.now = e.now.Add(30e9)
	e.acct(t, rfc2866.AcctStatusType_Value_Stop, "alice", "s1", 120, 130, 1500) // late repeat of the Stop
	if in, out, secs := e.used(t); in != 130 || out != 1500 || secs != 90 {
		t.Fatalf("used %d/%d/%d s, want 130/1500/90", in, out, secs)
	}
	if _, err := e.conn.Exec("DELETE FROM radius_sessions"); err != nil {
		t.Fatal(err)
	}
	if in, out, secs := e.used(t); in != 130 || out != 1500 || secs != 90 {
		t.Fatalf("pruning the log changed the counters: %d/%d/%d", in, out, secs)
	}
}

// Only sessions that started inside the usage window count, over both login names.
func TestUsageCountersWindowAndLogins(t *testing.T) {
	e := setup(t)
	if _, err := e.conn.Exec("UPDATE customers SET pppoe_username = 'alice-ppp'"); err != nil {
		t.Fatal(err)
	}
	e.acct(t, rfc2866.AcctStatusType_Value_InterimUpdate, "alice", "old", 500, 7, 7) // started before the window
	e.acct(t, rfc2866.AcctStatusType_Value_InterimUpdate, "alice-ppp", "p1", 10, 20, 30)
	e.acct(t, rfc2866.AcctStatusType_Value_InterimUpdate, "bob", "b1", 10, 99, 99) // someone else
	if in, out, secs := e.used(t); in != 20 || out != 30 || secs != 10 {
		t.Fatalf("used %d/%d/%d s, want 20/30/10", in, out, secs)
	}
}

// Accounting-On closes the NAS sessions behind the cache. The time between the last update and the
// close is not counted (the session ended somewhere in it), and a late packet of the closed session
// still adds its bytes but no more time.
func TestUsageCountersAfterAccountingOn(t *testing.T) {
	e := setup(t)
	e.acct(t, rfc2866.AcctStatusType_Value_InterimUpdate, "alice", "s1", 10, 10, 10)
	e.now = e.now.Add(60e9)
	e.acct(t, rfc2866.AcctStatusType_Value_AccountingOn, "", "", 0, 0, 0)
	e.now = e.now.Add(60e9)
	e.acct(t, rfc2866.AcctStatusType_Value_InterimUpdate, "alice", "s1", 130, 40, 40)
	in, _, secs := e.used(t)
	if in != 40 || secs != 10 {
		t.Fatalf("used %d bytes %d s, want 40 and 10", in, secs)
	}
}
