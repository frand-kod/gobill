package web

import (
	"github.com/frand-kod/gobill/internal/db"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// postFrom posts a form as a TCP peer; hdr are extra request headers.
func postFrom(h http.Handler, peer, path string, form url.Values, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = peer
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAttackerIPCannotLockAdminElsewhere(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	bad := url.Values{"username": {"alice"}, "password": {"wrong"}}
	for i := 0; i < maxFailedLogins; i++ {
		postFrom(h, "198.51.100.9:1", "/admin/login", bad)
	}
	// same-IP brute force stays blocked, even with the right password
	good := url.Values{"username": {"alice"}, "password": {"secret123"}}
	if w := postFrom(h, "198.51.100.9:1", "/admin/login", good); w.Code != http.StatusTooManyRequests {
		t.Fatalf("attacker IP not blocked: %d", w.Code)
	}
	// the admin from another IP still gets in
	if w := postFrom(h, "203.0.113.5:1", "/admin/login", good); w.Code != http.StatusSeeOther {
		t.Fatalf("admin locked out by another IP: %d", w.Code)
	}
}

func TestUserDelayGrowsAndCaps(t *testing.T) {
	s, _ := newTestApp(t)
	oldStep, oldMax := loginDelayStep, loginDelayMax
	loginDelayStep, loginDelayMax = 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { loginDelayStep, loginDelayMax = oldStep, oldMax })
	for i := 0; i < maxFailedLogins; i++ {
		s.recordFailure("u:alice")
	}
	start := time.Now()
	s.userDelay("u:alice")
	if d := time.Since(start); d < 50*time.Millisecond || d > time.Second {
		t.Fatalf("delay %v, want the 50ms cap", d)
	}
	if s.lockedCount() != 0 {
		t.Fatalf("username key counted as locked: %d", s.lockedCount())
	}
}

func TestProxyHeadersWarnWhenTrustOff(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	form := url.Values{"username": {"x"}, "password": {"y"}}
	postFrom(h, "198.51.100.9:1", "/admin/login", form, "X-Forwarded-For", "1.2.3.4")
	if s.proxyHint.Load() != 0 {
		t.Fatal("non-loopback peer flagged")
	}
	postFrom(h, "127.0.0.1:1", "/admin/login", form)
	if s.proxyHint.Load() != 0 {
		t.Fatal("plain loopback request flagged")
	}
	for _, hd := range []string{"CF-Connecting-IP", "X-Real-IP", "Forwarded", "X-Forwarded-For"} {
		s.proxyHint.Store(0)
		postFrom(h, "127.0.0.1:1", "/admin/login", form, hd, "1.2.3.4")
		if s.proxyHint.Load() == 0 {
			t.Fatalf("%s from loopback not noted", hd)
		}
	}
	if s.proxyWarning(nil) == "" {
		t.Fatal("no warning")
	}
	s.trust.Store(true)
	if s.proxyWarning(nil) != "" {
		t.Fatal("warning with trust_proxy=yes")
	}
}

// portalCGNAT returns a handler with customers ann and bob (password "pw123456").
func portalCGNAT(t *testing.T) (http.Handler, *Server) {
	s, q := newTestApp(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw123456"), bcrypt.MinCost)
	for _, u := range []string{"ann", "bob"} {
		if _, err := q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: u, PasswordHash: string(hash), Fullname: u, ServiceType: "Others", Status: "Active"}); err != nil {
			t.Fatal(err)
		}
	}
	oldStep := loginDelayStep
	loginDelayStep = 0
	t.Cleanup(func() { loginDelayStep = oldStep })
	return s.Handler(), s
}

func plogin(h http.Handler, peer, user, pw string) int {
	return postFrom(h, peer, "/login", url.Values{"username": {user}, "password": {pw}}).Code
}

func TestPortalLockIsPerUserBehindCGNAT(t *testing.T) {
	h, s := portalCGNAT(t)
	const nat = "198.51.100.9:1"
	for i := 0; i < maxFailedLogins; i++ {
		plogin(h, nat, " ANN ", "wrong")
	}
	// correct password still refused while the window runs (username is normalized)
	if c := plogin(h, nat, "ann", "pw123456"); c != http.StatusTooManyRequests {
		t.Fatalf("locked user got %d", c)
	}
	// bob on the same IP is unaffected
	if c := plogin(h, nat, "bob", "pw123456"); c != http.StatusSeeOther {
		t.Fatalf("innocent user on shared IP got %d", c)
	}
	// once the window passes, ann can log in again
	s.mu.Lock()
	for k, v := range s.failed {
		for i := range v {
			v[i] = v[i].Add(-loginWindow - time.Second)
		}
		s.failed[k] = v
	}
	s.mu.Unlock()
	if c := plogin(h, nat, "ann", "pw123456"); c != http.StatusSeeOther {
		t.Fatalf("still locked after window: %d", c)
	}
}

func TestPortalPerIPCapStopsSpraying(t *testing.T) {
	h, _ := portalCGNAT(t)
	const nat = "198.51.100.9:1"
	for i := 0; i < maxFailedPerIP; i++ {
		plogin(h, nat, "ghost"+strconv.Itoa(i), "wrong")
	}
	if c := plogin(h, nat, "bob", "pw123456"); c != http.StatusTooManyRequests {
		t.Fatalf("sprayer IP not capped: %d", c)
	}
	if c := plogin(h, "203.0.113.5:1", "bob", "pw123456"); c != http.StatusSeeOther {
		t.Fatalf("other IP got %d", c)
	}
}
