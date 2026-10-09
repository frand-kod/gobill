package web

// Maintenance-mode middleware.

import (
	"net/http"

	"html"
	"strings"
)

// maintenance answers 503 for the customer portal and other public pages while the setting
// maintenance_mode is "yes". Admin pages, /login, /logout, static files and /callback/ (payment gateway) keep working.
// With maintenance_mode_logout "yes", customer sessions are dropped as well. Must sit inside
// the session middleware.
func (s *Server) maintenance(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/callback/") || isRadiusRest(p) {
			next.ServeHTTP(w, r)
			return
		}
		settings, err := s.loadSettings(r.Context())
		if err != nil || settings["maintenance_mode"] != "yes" {
			next.ServeHTTP(w, r)
			return
		}
		if settings["maintenance_mode_logout"] == "yes" {
			s.sessions.Remove(r.Context(), "customer_id")
		}
		if p == "/" || p == "/admin" || strings.HasPrefix(p, "/admin/") || p == "/login" || p == "/logout" {
			next.ServeHTTP(w, r)
			return
		}
		title := html.EscapeString(s.catalog.T(s.language(), "Under Maintenance"))
		msg := html.EscapeString(s.catalog.T(s.language(), "We are performing maintenance. Please try again later."))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
			`<title>` + title + `</title></head>` +
			`<body style="font-family:sans-serif;text-align:center;padding:4rem 1rem"><h1>` + title + `</h1><p>` + msg + `</p></body></html>`))
	})
}
