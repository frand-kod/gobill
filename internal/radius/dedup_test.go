package radius

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
)

var natAddr = &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5000}

func TestDuplicateAuthSameResponseProcessedOnce(t *testing.T) {
	e := setup(t)
	req := pap("alice", "bad")
	var wires [][]byte
	for i := 0; i < 2; i++ {
		rc := &rec{}
		e.s.HandleAuth(rc, &radius.Request{Packet: req, RemoteAddr: natAddr})
		b, err := rc.p.Encode()
		if err != nil {
			t.Fatal(err)
		}
		wires = append(wires, b)
	}
	if !bytes.Equal(wires[0], wires[1]) {
		t.Fatal("duplicate got a different response")
	}
	if n := len(e.s.userFails.recent("alice", e.now.Unix())); n != 1 {
		t.Fatalf("processed %d times", n)
	}
	// retransmits never trip the throttle
	for i := 0; i < 20; i++ {
		rc := &rec{}
		e.s.HandleAuth(rc, &radius.Request{Packet: req, RemoteAddr: natAddr})
	}
	if r := e.auth(t, pap("alice", "pw")); r.Code != radius.CodeAccessAccept {
		t.Fatal("retransmits locked the user out")
	}
}

func TestDuplicateAcctUpdatesOnce(t *testing.T) {
	e := setup(t)
	mk := func(in uint32) *radius.Request {
		p := radius.New(radius.CodeAccountingRequest, nasSecret)
		rfc2866.AcctStatusType_Set(p, rfc2866.AcctStatusType_Value_InterimUpdate)
		rfc2866.AcctSessionID_SetString(p, "s1")
		rfc2865.UserName_SetString(p, "alice")
		rfc2866.AcctInputOctets_Set(p, rfc2866.AcctInputOctets(in))
		return &radius.Request{Packet: p, RemoteAddr: natAddr}
	}
	first := mk(100)
	rc := &rec{}
	e.s.HandleAcct(rc, first)
	// A stale retransmit arriving after the NAS moved on must not overwrite newer counters.
	e.s.HandleAcct(&rec{}, mk(500))
	e.s.HandleAcct(rc, first)
	var in int64
	e.conn.QueryRow("SELECT input_octets FROM radius_sessions WHERE session_id='s1'").Scan(&in)
	if in != 500 {
		t.Fatalf("duplicate re-applied: input %d", in)
	}
	if rc.p == nil || rc.p.Code != radius.CodeAccountingResponse {
		t.Fatal("no response to duplicate")
	}
}

func TestDupCacheExpiresAndCaps(t *testing.T) {
	e := setup(t)
	req := pap("alice", "bad")
	r := &radius.Request{Packet: req, RemoteAddr: natAddr}
	e.s.HandleAuth(&rec{}, r)
	e.now = e.now.Add(dupTTL + time.Second)
	e.s.HandleAuth(&rec{}, r)
	if n := len(e.s.userFails.recent("alice", e.now.Unix())); n != 2 {
		t.Fatalf("expired entry still deduped: %d", n)
	}
	var d dupCache
	now := time.Unix(1, 0)
	for i := 0; i < dupMax+5; i++ {
		d.serve(now, &rec{}, &radius.Request{Packet: radius.New(radius.CodeAccessRequest, nasSecret), RemoteAddr: &net.UDPAddr{Port: i}},
			func(w radius.ResponseWriter, r *radius.Request) { w.Write(r.Response(radius.CodeAccessReject)) })
	}
	if len(d.m) > dupMax {
		t.Fatalf("cache grew to %d", len(d.m))
	}
}

func TestUserThrottle(t *testing.T) {
	e := setup(t)
	for i := 0; i < voucherMaxFails; i++ {
		if r := e.auth(t, pap("alice", "bad")); rfc2865.ReplyMessage_GetString(r) != badPassword {
			t.Fatalf("attempt %d: %s", i, rfc2865.ReplyMessage_GetString(r))
		}
	}
	if r := e.auth(t, pap("alice", "pw")); rfc2865.ReplyMessage_GetString(r) != "Too many attempts, try again later" {
		t.Fatalf("11th not throttled: %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
	}
	if _, err := e.conn.Exec(`INSERT INTO customers (username,fullname,password_hash,service_type,secret_enc,status) SELECT 'bob','B',password_hash,service_type,secret_enc,status FROM customers WHERE username='alice'`); err != nil {
		t.Fatal(err)
	}
	if r := e.auth(t, pap("bob", "pw")); rfc2865.ReplyMessage_GetString(r) == "Too many attempts, try again later" {
		t.Fatal("other user throttled")
	}
	e.now = e.now.Add(16 * time.Minute)
	if r := e.auth(t, pap("alice", "pw")); r.Code != radius.CodeAccessAccept {
		t.Fatal("window did not expire")
	}
	// success resets the counter
	for i := 0; i < voucherMaxFails-1; i++ {
		e.auth(t, pap("alice", "bad"))
	}
	e.auth(t, pap("alice", "pw"))
	for i := 0; i < voucherMaxFails-1; i++ {
		e.auth(t, pap("alice", "bad"))
	}
	if r := e.auth(t, pap("alice", "pw")); r.Code != radius.CodeAccessAccept {
		t.Fatal(fmt.Sprint("success did not reset: ", rfc2865.ReplyMessage_GetString(r)))
	}
}
