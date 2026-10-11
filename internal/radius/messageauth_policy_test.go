package radius

import (
	"crypto/hmac"
	"crypto/md5"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2869"
)

// signedDouble is an Access-Request with two Message-Authenticator attributes, both holding the
// correct HMAC, so only the duplicate count can reject it.
func signedDouble() *radius.Packet {
	p := pap("alice", "pw")
	p.Attributes = append(radius.Attributes{
		{Type: rfc2869.MessageAuthenticator_Type, Attribute: make([]byte, 16)},
		{Type: rfc2869.MessageAuthenticator_Type, Attribute: make([]byte, 16)},
	}, p.Attributes...)
	b, _ := p.MarshalBinary()
	m := hmac.New(md5.New, nasSecret)
	m.Write(b)
	sum := m.Sum(nil)
	copy(p.Attributes[0].Attribute, sum)
	copy(p.Attributes[1].Attribute, sum)
	return p
}

// malformedMA carries a Message-Authenticator of 15 bytes.
func malformedMA() *radius.Packet {
	p := pap("alice", "pw")
	p.Attributes = append(radius.Attributes{{Type: rfc2869.MessageAuthenticator_Type, Attribute: make([]byte, 15)}}, p.Attributes...)
	return p
}

func TestMessageAuthPolicy(t *testing.T) {
	e := setup(t)
	set := func(policy int) {
		t.Helper()
		if _, err := e.conn.Exec("UPDATE nas SET require_message_auth = ?, ma_seen = 0", policy); err != nil {
			t.Fatal(err)
		}
	}
	seen := func() int64 {
		t.Helper()
		var v int64
		if err := e.conn.QueryRow("SELECT ma_seen FROM nas").Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	accepted := func(p *radius.Packet) bool {
		r := e.auth(t, p)
		return r != nil && r.Code == radius.CodeAccessAccept
	}

	set(0) // auto, never seen: no Message-Authenticator is fine
	if !accepted(pap("alice", "pw")) || seen() != 0 {
		t.Fatal("auto, not seen: missing M-A must pass and not mark seen")
	}
	if !accepted(signedPAP(true)) || seen() != 1 { // a valid one marks the NAS
		t.Fatal("auto, valid M-A must pass and mark seen")
	}
	if accepted(pap("alice", "pw")) {
		t.Fatal("auto, seen: missing M-A must be dropped")
	}
	set(1)
	if accepted(pap("alice", "pw")) {
		t.Fatal("always: missing M-A must be dropped")
	}
	if !accepted(signedPAP(true)) {
		t.Fatal("always: valid M-A must pass")
	}
	set(2)
	if !accepted(pap("alice", "pw")) {
		t.Fatal("never: missing M-A must pass")
	}
	for _, policy := range []int{0, 1, 2} {
		set(policy)
		if accepted(signedPAP(false)) {
			t.Fatalf("policy %d: invalid M-A must be dropped", policy)
		}
		if accepted(signedDouble()) {
			t.Fatalf("policy %d: duplicate M-A must be dropped", policy)
		}
		if accepted(malformedMA()) {
			t.Fatalf("policy %d: malformed M-A must be dropped", policy)
		}
		if seen() != 0 {
			t.Fatalf("policy %d: invalid M-A must not mark seen", policy)
		}
	}
}
