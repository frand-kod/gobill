package radius

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2869"
)

// independent RFC 3579 check on wire bytes: zero the M-A value (first attribute), HMAC-MD5.
func wireMAOK(t *testing.T, wire, reqAuth, secret []byte) bool {
	t.Helper()
	if wire[20] != 80 || wire[21] != 18 {
		t.Fatalf("Message-Authenticator is not the first attribute: % x", wire[20:22])
	}
	b := append([]byte(nil), wire...)
	copy(b[4:20], reqAuth) // M-A covers the Request Authenticator, not the Response one
	copy(b[22:38], make([]byte, 16))
	m := hmac.New(md5.New, secret)
	m.Write(b)
	return bytes.Equal(m.Sum(nil), wire[22:38])
}

func TestMessageAuthVector(t *testing.T) {
	// vector computed by hand with Python hmac: Access-Reject id 5, request auth 01..10,
	// M-A zeroed + Reply-Message "no", secret "testing123".
	p := &radius.Packet{Code: radius.CodeAccessReject, Identifier: 5, Secret: []byte("testing123")}
	copy(p.Authenticator[:], []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	rfc2865.ReplyMessage_SetString(p, "no")
	addMA(p)
	if got := hex.EncodeToString(rfc2869.MessageAuthenticator_Get(p)); got != "d990ec330bc47a04476eca76fdeb8fff" {
		t.Fatalf("M-A %s", got)
	}
}

func TestResponsesCarryMessageAuth(t *testing.T) {
	e := setup(t)
	for _, c := range []struct {
		pass string
		want radius.Code
	}{{"pw", radius.CodeAccessAccept}, {"bad", radius.CodeAccessReject}} {
		req := pap("alice", c.pass)
		r := e.auth(t, req)
		if r.Code != c.want {
			t.Fatalf("code %v", r.Code)
		}
		wire, err := r.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !wireMAOK(t, wire, req.Authenticator[:], nasSecret) {
			t.Fatalf("%v: bad Message-Authenticator", c.want)
		}
		reqWire, _ := req.Encode()
		if !radius.IsAuthenticResponse(wire, reqWire, nasSecret) {
			t.Fatalf("%v: bad Response Authenticator", c.want)
		}
	}
}

func signedPAP(good bool) *radius.Packet {
	p := pap("alice", "pw")
	p.Attributes = append(radius.Attributes{{Type: rfc2869.MessageAuthenticator_Type, Attribute: make([]byte, 16)}}, p.Attributes...)
	b, _ := p.MarshalBinary()
	m := hmac.New(md5.New, nasSecret)
	m.Write(b)
	copy(p.Attributes[0].Attribute, m.Sum(nil))
	if !good {
		p.Attributes[0].Attribute[0] ^= 1
	}
	return p
}

func TestRequestMessageAuth(t *testing.T) {
	e := setup(t)
	if r := e.auth(t, signedPAP(true)); r == nil || r.Code != radius.CodeAccessAccept {
		t.Fatalf("valid M-A: %+v", r)
	}
	if r := e.auth(t, signedPAP(false)); r != nil {
		t.Fatal("tampered M-A must be dropped")
	}
	if r := e.auth(t, pap("alice", "pw")); r == nil || r.Code != radius.CodeAccessAccept {
		t.Fatal("no M-A must pass when not required")
	}
	if _, err := e.conn.Exec(`UPDATE nas SET require_message_auth = 1`); err != nil {
		t.Fatal(err)
	}
	if r := e.auth(t, pap("alice", "pw")); r != nil {
		t.Fatal("missing M-A must be dropped when required")
	}
	if r := e.auth(t, signedPAP(true)); r == nil || r.Code != radius.CodeAccessAccept {
		t.Fatal("valid M-A must pass when required")
	}
}
