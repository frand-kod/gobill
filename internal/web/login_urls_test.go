package web

import (
	"net/http"
	"testing"
)

// The customer login is /login and the admin login is /admin/login. /portal/login is gone.
func TestLoginURLs(t *testing.T) {
	h, _ := newTestServer(t)
	for _, p := range []string{"/login", "/admin/login"} {
		if w := do(h, "GET", p, nil, nil); w.Code != http.StatusOK {
			t.Errorf("GET %s: %d, want 200", p, w.Code)
		}
	}
	if w := do(h, "GET", "/portal/login", nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("GET /portal/login: %d, want 404", w.Code)
	}
}

func TestRootRedirect(t *testing.T) {
	e := billApp(t)
	if w := do(e.h, "GET", "/", nil, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Errorf("anonymous /: %d %q, want 303 /login", w.Code, w.Header().Get("Location"))
	}
	portalCust(t, e, 0)
	c, code := custLogin(t, e, e.cust.Username, "pw12345")
	if c == nil {
		t.Fatalf("customer login: %d", code)
	}
	if w := do(e.h, "GET", "/", nil, c); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal" {
		t.Errorf("customer /: %d %q, want 303 /portal", w.Code, w.Header().Get("Location"))
	}
}

func TestNotLoggedInRedirects(t *testing.T) {
	h, _ := newTestServer(t)
	if w := do(h, "GET", "/admin", nil, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/login" {
		t.Errorf("GET /admin: %d %q, want 303 /admin/login", w.Code, w.Header().Get("Location"))
	}
	if w := do(h, "GET", "/portal", nil, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Errorf("GET /portal: %d %q, want 303 /login", w.Code, w.Header().Get("Location"))
	}
}

func TestCustomerLoginURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://billing.example.com":  "https://billing.example.com/login",
		"https://billing.example.com/": "https://billing.example.com/login",
		"":                             "",
	} {
		if got := customerLoginURL(map[string]string{"app_url": in}); got != want {
			t.Errorf("customerLoginURL(%q) = %q, want %q", in, got, want)
		}
	}
}
