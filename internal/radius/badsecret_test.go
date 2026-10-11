package radius

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"

	"github.com/frand-kod/gobill/internal/metrics"
)

// accountingWire is an Accounting-Request signed with secret, as a NAS would send it.
func accountingWire(t *testing.T, secret []byte) []byte {
	t.Helper()
	p := radius.New(radius.CodeAccountingRequest, secret)
	rfc2866.AcctSessionID_SetString(p, "s1")
	wire, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestBadSecretLoggedOncePerMinuteAndCounted(t *testing.T) {
	metrics.Reset()
	t.Cleanup(metrics.Reset)
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	e := setup(t) // NAS n1 = 10.0.0.0/24 with nasSecret
	from := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1813}
	const ip = "10.0.0.1"
	bad := accountingWire(t, []byte("wrong-secret"))

	for i := 0; i < 2; i++ {
		if e.s.verifySource(bad, from) {
			t.Fatal("packet with the wrong secret reached the server")
		}
	}
	if n := strings.Count(buf.String(), "bad secret"); n != 1 {
		t.Fatalf("want 1 log line in a minute, got %d", n)
	}
	if !strings.Contains(buf.String(), "nas=n1") || !strings.Contains(buf.String(), "ip=10.0.0.1") {
		t.Fatalf("log lacks NAS name or IP: %s", buf.String())
	}
	if v := metrics.Value("radius_bad_secret_total", "nas", ip); v != 2 {
		t.Fatalf("counter %v, want 2", v)
	}
	if v := metrics.Value("radius_bad_secret_streak", "nas", ip); v != 2 {
		t.Fatalf("streak %v, want 2", v)
	}

	e.now = e.now.Add(time.Minute + time.Second)
	e.s.verifySource(bad, from)
	if n := strings.Count(buf.String(), "bad secret"); n != 2 {
		t.Fatalf("want a second log after a minute, got %d", n)
	}

	if !e.s.verifySource(accountingWire(t, nasSecret), from) {
		t.Fatal("valid signature dropped")
	}
	if v := metrics.Value("radius_bad_secret_streak", "nas", ip); v != 0 {
		t.Fatalf("valid packet must end the streak, got %v", v)
	}
	if v := metrics.Value("radius_bad_secret_total", "nas", ip); v != 3 {
		t.Fatalf("counter must not move for a valid packet, got %v", v)
	}
}

func TestBadSecretIgnoresUnknownNASAndAccessRequest(t *testing.T) {
	metrics.Reset()
	t.Cleanup(metrics.Reset)
	e := setup(t)
	bad := accountingWire(t, []byte("wrong-secret"))
	if !e.s.verifySource(bad, &net.UDPAddr{IP: net.ParseIP("192.168.9.9"), Port: 1813}) {
		t.Fatal("unknown NAS must be left to RADIUSSecret, not dropped here")
	}
	req, _ := pap("alice", "pw").Encode() // Access-Request has no signature to check here
	if !e.s.verifySource(req, &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1812}) {
		t.Fatal("Access-Request must not be judged by the signature check")
	}
	if n := metrics.Value("radius_bad_secret_total", "nas", "10.0.0.1"); n != 0 {
		t.Fatalf("counted %v bad secrets", n)
	}
}

func TestBadMessageAuthCountsAsBadSecret(t *testing.T) {
	metrics.Reset()
	t.Cleanup(metrics.Reset)
	e := setup(t)
	// Access-Request whose Message-Authenticator is computed with the wrong secret.
	p := radius.New(radius.CodeAccessRequest, []byte("wrong-secret"))
	rfc2865.UserName_SetString(p, "alice")
	rfc2865.UserPassword_SetString(p, "pw")
	p.Attributes = append(radius.Attributes{{Type: rfc2869.MessageAuthenticator_Type, Attribute: make([]byte, 16)}}, p.Attributes...)
	copy(p.Attributes[0].Attribute, maSum(p))
	p.Secret = nasSecret // what the server sees after parsing with the NAS secret
	if r := e.auth(t, p); r != nil {
		t.Fatal("wrong Message-Authenticator must be dropped")
	}
	if v := metrics.Value("radius_bad_secret_total", "nas", "10.0.0.1"); v != 1 {
		t.Fatalf("counter %v, want 1", v)
	}
}
