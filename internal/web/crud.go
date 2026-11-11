package web

// Generic CRUD helpers: ID parsing, bulk delete, activity log and error pages.

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"

	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"html"
	"strconv"
	"strings"
)

const maxBulkIDs = 1000

// parseIDs reads the posted "ids" strictly: positive integers only, duplicates dropped, 1..maxBulkIDs of them.
func parseIDs(r *http.Request) ([]int64, bool) {
	r.ParseForm()
	vals := r.PostForm["ids"]
	if len(vals) == 0 || len(vals) > maxBulkIDs {
		return nil, false
	}
	seen := map[int64]bool{}
	var ids []int64
	for _, v := range vals {
		id, ok := posInt(v)
		if !ok {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, true
}

// bulkDelete runs del over the posted ids in ONE transaction, logs once and flashes "N Data Deleted Successfully".
// A foreign-key refusal rolls everything back and shows inUse.
func (s *Server) bulkDelete(w http.ResponseWriter, r *http.Request, back, what, inUse string, del func(q *db.Queries, ids []int64) (int64, error)) {
	ids, ok := parseIDs(r)
	if !ok {
		s.sessions.Put(r.Context(), "error", s.t("Select at least one row"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	tx, err := s.conn.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, "begin bulk delete "+what, err)
		return
	}
	defer tx.Rollback()
	n, err := del(s.queries.WithTx(tx), ids)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if !isFK(err) {
			s.fail(w, "bulk delete "+what, err)
			return
		}
		s.sessions.Put(r.Context(), "error", s.t(inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.bulkDone(w, r, back, what+".delete_many", n)
}

// bulkDone logs one activity entry with the count and flashes "N Data Deleted Successfully".
func (s *Server) bulkDone(w http.ResponseWriter, r *http.Request, back, action string, n int64) {
	s.logActivity(r, action, fmt.Sprint(n))
	s.sessions.Put(r.Context(), "flash", fmt.Sprintf("%d %s", n, s.t("Data Deleted Successfully")))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// posInt parses a positive integer.
func posInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

func oneOf(v string, list ...string) bool { return contains(list, v) }

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func isFK(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	ref := make([]byte, 4)
	rand.Read(ref)
	slog.Error(what, "err", err, "ref", hex.EncodeToString(ref))
	s.errorPage(w, hex.EncodeToString(ref))
}

// errorPage is the 500 page: Indonesian text from the catalog, no internal error text, a reference to find the log line.
func (s *Server) errorPage(w http.ResponseWriter, ref string) {
	s.errorHTML(w, http.StatusInternalServerError, "Something went wrong", "The request could not be completed. Check the data before trying again.", ref)
}

// langCode is the html lang attribute for the active language.
func (s *Server) langCode() string {
	switch s.language() {
	case "english":
		return "en"
	case "arabic":
		return "ar"
	case "spanish":
		return "es"
	case "turkish":
		return "tr"
	}
	return "id"
}

// errorHTML is the shared error page (500, 403, 404, 405). It does not use the page layout because fail has
// no request (and the failure may be in the layout itself), so it carries its own lang and a small theme script.
func (s *Server) errorHTML(w http.ResponseWriter, code int, title, msg, ref string) {
	t := func(k string) string { return html.EscapeString(s.t(k)) }
	refLine := ""
	if ref != "" {
		refLine = `<p class="hint">Ref: ` + html.EscapeString(ref) + `</p>`
	}
	dir := "ltr"
	if s.language() == "arabic" {
		dir = "rtl"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><html lang="%[6]s" dir="%[7]s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%[1]s</title>`+
		`<script>try{var t=JSON.parse(localStorage.getItem('gb-theme')||'{}'),m=t.mode||'system',d=document.documentElement;d.classList.toggle('dark',m==='dark'||m==='system'&&matchMedia('(prefers-color-scheme: dark)').matches);if(t.accent==='orange')d.dataset.accent='orange'}catch(e){}</script>`+
		`<link rel="stylesheet" href="%[5]s"></head>
<body><main class="mx-auto max-w-md p-4"><div class="card mt-12"><div class="card-body space-y-4" role="alert"><h1 class="text-xl font-semibold">%[1]s</h1><p>%[2]s</p>%[3]s
<a class="btn btn-primary min-h-10" href="/" onclick="history.back();return false">%[4]s</a></div></div></main></body></html>`,
		t(title), t(msg), refLine, t("Go back"), s.asset("/static/app.css"), s.langCode(), dir)
}

// wantsPlain reports a request whose answer is data, not a page: API, JSON, RADIUS and static paths.
func wantsPlain(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasSuffix(p, ".json") || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/static/") ||
		strings.HasPrefix(p, "/callback/") || isRadiusRest(p) || strings.Contains(r.Header.Get("Accept"), "json")
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
	s.errorHTML(w, code, title, msg, "")
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

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// logActivity records an admin action. A failure is logged, never shown.
func (s *Server) logActivity(r *http.Request, action, desc string) {
	err := s.queries.CreateActivityLog(r.Context(), db.CreateActivityLogParams{
		ActorType: "admin", ActorID: adminFrom(r).ID, Action: action, Description: desc, Ip: clientIP(r),
	})
	if err != nil {
		slog.Error("activity log", "err", err)
	}
}

// done flashes a message, logs the action and redirects.
func (s *Server) done(w http.ResponseWriter, r *http.Request, back, msg, action, desc string) {
	s.logActivity(r, action, desc)
	s.sessions.Put(r.Context(), "flash", s.t(msg))
	if strings.Contains(action, "recharge") || strings.Contains(action, "deposit") { // money results stay until closed
		s.sessions.Put(r.Context(), "flash_sticky", "1")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// remove runs a delete; a foreign-key refusal becomes the friendly message inUse.
func (s *Server) remove(w http.ResponseWriter, r *http.Request, back, what, inUse, name string, del func(id int64) error) {
	if err := del(pathID(r)); err != nil {
		if !isFK(err) {
			s.fail(w, "delete "+what, err)
			return
		}
		s.sessions.Put(r.Context(), "error", s.t(inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.done(w, r, back, "Data Deleted Successfully", what+".delete", name)
}
