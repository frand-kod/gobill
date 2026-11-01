package radius

import (
	"context"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"

	"github.com/frand-kod/gobill/internal/db"
)

func TestInterimIntervalSent(t *testing.T) {
	e := setup(t)
	r := e.auth(t, pap("alice", "pw"))
	if got := rfc2869.AcctInterimInterval_Get(r); r.Code != radius.CodeAccessAccept || got != 300 {
		t.Fatalf("default: %v interval %d", r.Code, got)
	}
	if _, err := e.conn.Exec("INSERT INTO settings (key, value) VALUES ('radius_interim_interval', '120')"); err != nil {
		t.Fatal(err)
	}
	e.s.forgetNAS()
	p := pap("alice", "pw")
	p.Identifier++
	if got := rfc2869.AcctInterimInterval_Get(e.auth(t, p)); got != 120 {
		t.Fatalf("setting: %d", got)
	}
	if _, err := e.conn.Exec("UPDATE settings SET value = '0' WHERE key = 'radius_interim_interval'"); err != nil {
		t.Fatal(err)
	}
	e.s.forgetNAS()
	p = pap("alice", "pw")
	p.Identifier += 2
	if _, err := rfc2869.AcctInterimInterval_Lookup(e.auth(t, p)); err == nil {
		t.Fatal("0 must not send the attribute")
	}
}

func TestIdleTimeoutPerPlan(t *testing.T) {
	e := setup(t)
	if _, err := rfc2865.IdleTimeout_Lookup(e.auth(t, pap("alice", "pw"))); err == nil {
		t.Fatal("unset plan must not send Idle-Timeout")
	}
	if _, err := e.conn.Exec("UPDATE plans SET idle_timeout = 600"); err != nil {
		t.Fatal(err)
	}
	p := pap("alice", "pw")
	p.Identifier++
	if got := rfc2865.IdleTimeout_Get(e.auth(t, p)); got != 600 {
		t.Fatalf("idle-timeout %d", got)
	}
}

func TestTerminateCauseStored(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := AcctRequest{Type: rfc2866.AcctStatusType_Value_Start, NAS: "10.0.0.1", SessionID: "s1", User: "alice"}
	if err := e.s.Account(ctx, a); err != nil {
		t.Fatal(err)
	}
	a.Type, a.TerminateCause = rfc2866.AcctStatusType_Value_Stop, rfc2866.AcctTerminateCause_Value_IdleTimeout.String()
	if err := e.s.Account(ctx, a); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := e.conn.QueryRow("SELECT terminate_cause FROM radius_sessions WHERE session_id = 's1'").Scan(&got); err != nil || got != "Idle-Timeout" {
		t.Fatalf("cause %q err %v", got, err)
	}
}

func TestAuthLogWritten(t *testing.T) {
	e := setup(t)
	e.auth(t, pap("alice", "pw"))
	p := pap("alice", "bad")
	p.Identifier++
	e.auth(t, p)
	rows, err := e.q.SearchRadiusAuthLog(context.Background(), db.SearchRadiusAuthLogParams{PageLimit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %v err %v", rows, err)
	}
	if r := rows[0]; r.Accepted != 0 || r.Reason != "bad_password" || r.Username != "alice" || r.NasIp != "10.0.0.1" {
		t.Fatalf("reject row %+v", r)
	}
	if rows[1].Accepted != 1 {
		t.Fatalf("accept row %+v", rows[1])
	}
	e.s.Authorize(context.Background(), AuthRequest{User: "alice", DryRun: true, Check: func([]byte) (bool, string) { return true, "" }})
	if n, _ := e.q.CountRadiusAuthLog(context.Background(), db.CountRadiusAuthLogParams{}); n != 2 {
		t.Fatalf("dry run must not log, have %d", n)
	}
}
