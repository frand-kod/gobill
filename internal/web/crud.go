package web

// Generic CRUD helpers: ID parsing, bulk delete, activity log and error pages.

import (
	"github.com/frand-kod/gobill/internal/web/public"
	"net/http"

	"strings"
)

// wantsPlain reports a request whose answer is data, not a page: API, JSON, RADIUS and static paths.
func wantsPlain(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasSuffix(p, ".json") || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/static/") ||
		strings.HasPrefix(p, "/callback/") || public.IsRadiusRest(p) || strings.Contains(r.Header.Get("Accept"), "json")
}

// deny answers 403, 404 or 405: the styled page for browsers, the bare status text for API callers.
func (s *Server) deny(w http.ResponseWriter, r *http.Request, code int) {
	if wantsPlain(r) {
		http.Error(w, http.StatusText(code), code)
		return
	}
	title, msg := "Forbidden", "You do not have access to this page."
	switch code {
	case http.StatusNotFound:
		title, msg = "Page not found", "The page you opened does not exist or was moved."
	case http.StatusMethodNotAllowed:
		title, msg = "Method not allowed", "This address does not accept that kind of request."
	}
	s.ErrorHTML(w, code, title, msg, "")
}

// styledNotFound wraps the mux: a request no route matches (404, or 405 for a known path) gets the styled page.
func (s *Server) styledNotFound(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r) // not h: only the mux fills in the path values
			return
		}
		probe := &statusProbe{h: http.Header{}}
		h.ServeHTTP(probe, r)
		if probe.code != http.StatusNotFound && probe.code != http.StatusMethodNotAllowed { // a redirect to the clean path
			mux.ServeHTTP(w, r)
			return
		}
		if probe.code == http.StatusMethodNotAllowed {
			w.Header().Set("Allow", probe.h.Get("Allow"))
		}
		s.deny(w, r, probe.code)
	})
}

// statusProbe records the status the mux's own 404/405 handler would send, and drops its body.
type statusProbe struct {
	h    http.Header
	code int
}

func (p *statusProbe) Header() http.Header         { return p.h }
func (p *statusProbe) Write(b []byte) (int, error) { return len(b), nil }
func (p *statusProbe) WriteHeader(c int)           { p.code = c }
