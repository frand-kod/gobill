package radius

import (
	"context"
	"strings"
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

func TestPortTypeMismatchRejected(t *testing.T) {
	e := setup(t)
	withPort := func(id byte, pt rfc2865.NASPortType) *radius.Packet {
		p := pap("alice", "pw")
		p.Identifier = id
		rfc2865.NASPortType_Set(p, pt)
		return p
	}
	// alice has a Hotspot plan
	r := e.auth(t, withPort(1, rfc2865.NASPortType_Value_Virtual))
	if r.Code != radius.CodeAccessReject || !strings.Contains(rfc2865.ReplyMessage_GetString(r), "Port type mismatch") {
		t.Fatalf("hotspot on virtual: %v %q", r.Code, rfc2865.ReplyMessage_GetString(r))
	}
	for i, pt := range []rfc2865.NASPortType{rfc2865.NASPortType_Value_Wireless80211, rfc2865.NASPortType_Value_Ethernet} {
		if r := e.auth(t, withPort(byte(2+i), pt)); r.Code != radius.CodeAccessAccept {
			t.Fatalf("hotspot on %v: %v", pt, r.Code)
		}
	}
	p := pap("alice", "pw") // no NAS-Port-Type: the check is skipped
	p.Identifier = 9
	if r := e.auth(t, p); r.Code != radius.CodeAccessAccept {
		t.Fatalf("no port type: %v", r.Code)
	}
	// vice versa: the same customer on a PPPoE plan
	if _, err := e.conn.Exec("UPDATE plans SET type = 'PPPoE'"); err != nil {
		t.Fatal(err)
	}
	if r := e.auth(t, withPort(10, rfc2865.NASPortType_Value_Wireless80211)); r.Code != radius.CodeAccessReject {
		t.Fatalf("pppoe on wireless: %v", r.Code)
	}
	if r := e.auth(t, withPort(11, rfc2865.NASPortType_Value_Virtual)); r.Code != radius.CodeAccessAccept {
		t.Fatalf("pppoe on virtual: %v", r.Code)
	}
}

func TestBindingRejectsMismatch(t *testing.T) {
	e := setup(t)
	if _, err := e.conn.Exec("INSERT INTO customer_bindings (customer_id, mac, port_id) SELECT id, 'AA:BB:CC:DD:EE:FF', 'ether3' FROM customers"); err != nil {
		t.Fatal(err)
	}
	try := func(id byte, mac, port string) *radius.Packet {
		p := pap("alice", "pw")
		p.Identifier = id
		if mac != "" {
			rfc2865.CallingStationID_SetString(p, mac)
		}
		if port != "" {
			rfc2869.NASPortID_SetString(p, port)
		}
		return e.auth(t, p)
	}
	for i, c := range []struct{ mac, port, want string }{
		{"AA:BB:CC:DD:EE:FF", "ether3", ""},
		{"aa-bb-cc-dd-ee-ff", "ETHER3", ""},
		{"AA:BB:CC:DD:EE:00", "ether3", "MAC"},
		{"", "ether3", "MAC"},
		{"AA:BB:CC:DD:EE:FF", "ether4", "NAS-Port-Id"},
		{"AA:BB:CC:DD:EE:FF", "", "NAS-Port-Id"},
	} {
		r := try(byte(i+1), c.mac, c.port)
		msg := rfc2865.ReplyMessage_GetString(r)
		if c.want == "" && r.Code != radius.CodeAccessAccept || c.want != "" && (r.Code != radius.CodeAccessReject || !strings.Contains(msg, "Account locked") || !strings.Contains(msg, c.want)) {
			t.Errorf("%+v: %v %q", c, r.Code, msg)
		}
	}
	// MAC only: the port is free
	if _, err := e.conn.Exec("UPDATE customer_bindings SET port_id = ''"); err != nil {
		t.Fatal(err)
	}
	e.s.forgetNAS()
	if r := try(20, "AA:BB:CC:DD:EE:FF", "whatever"); r.Code != radius.CodeAccessAccept {
		t.Fatalf("mac-only binding: %v", r.Code)
	}
}

func TestNASPortIDStoredOnSession(t *testing.T) {
	e := setup(t)
	if err := e.s.Account(context.Background(), AcctRequest{Type: rfc2866.AcctStatusType_Value_Start, NAS: "10.0.0.1", SessionID: "s1", User: "alice", NASPortID: "ether3"}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := e.conn.QueryRow("SELECT nas_port_id FROM radius_sessions WHERE session_id = 's1'").Scan(&got); err != nil || got != "ether3" {
		t.Fatalf("port %q err %v", got, err)
	}
}
