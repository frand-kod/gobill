package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cookieFrom returns the session cookie the response set, or the old one when it set none.
func cookieFrom(w *httptest.ResponseRecorder, old *http.Cookie) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == old.Name {
			return c
		}
	}
	return old
}

// A prefetch reads the session without saving it: the flash and the one-time metrics token stay for the
// real navigation, and the prefetch response sets no cookie.
func TestPrefetchDoesNotConsumeSession(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	prefetch := []string{"Sec-Purpose", "prefetch;anonymous-client-ip"}

	c = cookieFrom(do(h, "POST", "/admin/settings/integrations/metrics-token", nil, c), c)
	pf := do(h, "GET", "/admin/settings/integrations", nil, c, prefetch...)
	if len(pf.Result().Cookies()) != 0 {
		t.Error("prefetch response must not set a cookie")
	}
	if real := do(h, "GET", "/admin/settings/integrations", nil, c).Body.String(); !strings.Contains(real, "scrape_configs:") {
		t.Error("one-time metrics token was consumed by a prefetch")
	}

	c = cookieFrom(do(h, "POST", "/admin/settings/integrations/metrics-token/disable", nil, c), c)
	do(h, "GET", "/admin/settings/integrations", nil, c, "Purpose", "prefetch")
	// the default language is Indonesian, so match the toast by its shared prefix
	if real := do(h, "GET", "/admin/settings/integrations", nil, c).Body.String(); !strings.Contains(real, `id="toasts"`) || !strings.Contains(real, "Monitoring /metrics") {
		t.Error("flash was consumed by a prefetch")
	}
}
