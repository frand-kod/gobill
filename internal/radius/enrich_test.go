package radius

import (
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2869"
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
