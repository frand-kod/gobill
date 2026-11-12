package public

import (
	"net/http"
	"testing"

	"github.com/frand-kod/gobill/internal/radius"
)

// The shared-users reject keeps the Auth-Type Accept semantics of the old PHP limit reject.
func TestRejectBodySharedUsers(t *testing.T) {
	b := rejectBody(radius.SharedUsersFullMsg)
	if b["control:Auth-Type"] != "Accept" || b["Reply-Message"] != radius.SharedUsersFullMsg {
		t.Fatalf("shared users reject: %v", b)
	}
	if b := rejectBody("Login invalid......"); b["control:Auth-Type"] != "Reject" {
		t.Fatalf("other reject: %v", b)
	}
}

// sec#6: with neither radius_rest_allow nor radius_rest_secret, nothing is accepted (not even
// loopback); the secret header opens it from any address; an explicit allow-list checks the TCP peer only.
func TestRadiusRestClosedByDefault(t *testing.T) {
	req := func(remote string, hdr ...string) *http.Request {
		r := &http.Request{RemoteAddr: remote, Header: http.Header{}}
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		return r
	}
	if allowed(req("127.0.0.1:1"), map[string]string{}) {
		t.Error("loopback accepted with no setting")
	}
	m := map[string]string{"radius_rest_secret": "s3cret"}
	for _, c := range []struct {
		r    *http.Request
		want bool
	}{
		{req("8.8.8.8:1", "X-Gobill-Secret", "s3cret"), true},
		{req("8.8.8.8:1", "X-Gobill-Secret", "wrong"), false},
		{req("127.0.0.1:1"), false},
		{req("8.8.8.8:1", "X-Gobill-Secret", ""), false},
	} {
		if got := allowed(c.r, m); got != c.want {
			t.Errorf("secret case %v: %v want %v", c.r.Header, got, c.want)
		}
	}
	if allowed(req("8.8.8.8:1", "X-Gobill-Secret", ""), map[string]string{}) {
		t.Error("empty secret header matched an unset secret")
	}
	// a same-host reverse proxy makes every client 127.0.0.1: a relayed request must not pass the allow-list
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "CF-Connecting-IP"} {
		if allowed(req("127.0.0.1:1", h, "203.0.113.9"), map[string]string{"radius_rest_allow": "127.0.0.1"}) {
			t.Errorf("%s: proxied request passed the loopback allow-list", h)
		}
	}
	// the secret still works through a proxy
	if !allowed(req("127.0.0.1:1", "X-Forwarded-For", "203.0.113.9", "X-Gobill-Secret", "s3cret"), m) {
		t.Error("secret refused behind a proxy")
	}
}

func allowed(r *http.Request, m map[string]string) bool { return radiusRestAllowed(r, m, r.RemoteAddr) }

func TestRestOctetsClamp(t *testing.T) {
	for _, c := range []struct{ g, o, want int64 }{
		{0, 100, 100},
		{1, 5, 1<<32 | 5},
		{-1, -5, 0},
		{1 << 40, 0, (1<<31 - 1) << 32},
		{0, 1 << 40, 1<<32 - 1},
	} {
		if got := restOctets(c.g, c.o); got != c.want || got < 0 {
			t.Errorf("restOctets(%d, %d) = %d, want %d", c.g, c.o, got, c.want)
		}
	}
}
