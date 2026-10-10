package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// postLogin posts a login with a chosen Host header, the way a browser on that address would.
func postLogin(h http.Handler, user, pass, host string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"username": {user}, "password": {pass}}.Encode()))
	r.Host = host
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAppURLFillsOnceOnAdminLogin(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	if w := postLogin(h, "alice", "wrong", "evil.test"); w.Code != http.StatusOK {
		t.Fatalf("failed login: %d", w.Code)
	}
	if st, _ := s.loadSettings(t.Context()); st["app_url"] != "" {
		t.Fatalf("failed login set app_url %q", st["app_url"])
	}
	if w := postLogin(h, "alice", "secret123", "billing.test"); w.Code != http.StatusSeeOther {
		t.Fatalf("login: %d", w.Code)
	}
	if st, _ := s.loadSettings(t.Context()); st["app_url"] != "https://billing.test" {
		t.Fatalf("app_url = %q", st["app_url"])
	}
	postLogin(h, "rita", "secret123", "other.test")
	if st, _ := s.loadSettings(t.Context()); st["app_url"] != "https://billing.test" {
		t.Fatalf("second login overwrote app_url: %q", st["app_url"])
	}
}
