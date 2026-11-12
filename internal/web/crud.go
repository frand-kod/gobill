package web

// Generic CRUD helpers: ID parsing, bulk delete, activity log and error pages.

import (
	"github.com/frand-kod/gobill/internal/web/public"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"fmt"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

// bulkDelete runs del over the posted ids in ONE transaction, logs once and flashes "N Data Deleted Successfully".
// A foreign-key refusal rolls everything back and shows inUse.
func (s *Server) bulkDelete(w http.ResponseWriter, r *http.Request, back, what, inUse string, del func(q *db.Queries, ids []int64) (int64, error)) {
	ids, ok := httpx.ParseIDs(r)
	if !ok {
		s.Sessions.Put(r.Context(), "error", s.T("Select at least one row"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	tx, err := s.conn.BeginTx(r.Context(), nil)
	if err != nil {
		s.Fail(w, "begin bulk delete "+what, err)
		return
	}
	defer tx.Rollback()
	n, err := del(s.Queries.WithTx(tx), ids)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if !httpx.IsFK(err) {
			s.Fail(w, "bulk delete "+what, err)
			return
		}
		s.Sessions.Put(r.Context(), "error", s.T(inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.bulkDone(w, r, back, what+".delete_many", n)
}

// bulkDone logs one activity entry with the count and flashes "N Data Deleted Successfully".
func (s *Server) bulkDone(w http.ResponseWriter, r *http.Request, back, action string, n int64) {
	s.logActivity(r, action, fmt.Sprint(n))
	s.Sessions.Put(r.Context(), "flash", fmt.Sprintf("%d %s", n, s.T("Data Deleted Successfully")))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

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

// logActivity records an admin action. A failure is logged, never shown.
func (s *Server) logActivity(r *http.Request, action, desc string) {
	err := s.Queries.CreateActivityLog(r.Context(), db.CreateActivityLogParams{
		ActorType: "admin", ActorID: adminFrom(r).ID, Action: action, Description: desc, Ip: core.ClientIP(r),
	})
	if err != nil {
		slog.Error("activity log", "err", err)
	}
}

// done flashes a message, logs the action and redirects.
func (s *Server) done(w http.ResponseWriter, r *http.Request, back, msg, action, desc string) {
	s.logActivity(r, action, desc)
	s.Sessions.Put(r.Context(), "flash", s.T(msg))
	if strings.Contains(action, "recharge") || strings.Contains(action, "deposit") { // money results stay until closed
		s.Sessions.Put(r.Context(), "flash_sticky", "1")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// remove runs a delete; a foreign-key refusal becomes the friendly message inUse.
func (s *Server) remove(w http.ResponseWriter, r *http.Request, back, what, inUse, name string, del func(id int64) error) {
	if err := del(httpx.PathID(r)); err != nil {
		if !httpx.IsFK(err) {
			s.Fail(w, "delete "+what, err)
			return
		}
		s.Sessions.Put(r.Context(), "error", s.T(inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.done(w, r, back, "Data Deleted Successfully", what+".delete", name)
}
