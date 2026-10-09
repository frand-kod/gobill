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
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Select at least one row"))
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
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.bulkDone(w, r, back, what+".delete_many", n)
}

// bulkDone logs one activity entry with the count and flashes "N Data Deleted Successfully".
func (s *Server) bulkDone(w http.ResponseWriter, r *http.Request, back, action string, n int64) {
	s.logActivity(r, action, fmt.Sprint(n))
	s.sessions.Put(r.Context(), "flash", fmt.Sprintf("%d %s", n, s.catalog.T(s.language(), "Data Deleted Successfully")))
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
// It does not use the page layout because fail has no request (and the failure may be in the layout itself).
func (s *Server) errorPage(w http.ResponseWriter, ref string) {
	t := func(k string) string { return html.EscapeString(s.catalog.T(s.language(), k)) }
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%[1]s</title><link rel="stylesheet" href="/static/app.css"></head>
<body><main class="mx-auto max-w-md p-4"><div class="card mt-12"><div class="card-body space-y-4" role="alert"><h1 class="text-xl font-semibold">%[1]s</h1><p>%[2]s</p><p class="hint">Ref: %[3]s</p>
<a class="btn btn-primary min-h-10" href="/" onclick="history.back();return false">%[4]s</a></div></div></main></body></html>`,
		t("Something went wrong"), t("The request could not be completed. Check the data before trying again."), ref, t("Go back"))
}

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
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), msg))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// remove runs a delete; a foreign-key refusal becomes the friendly message inUse.
func (s *Server) remove(w http.ResponseWriter, r *http.Request, back, what, inUse, name string, del func(id int64) error) {
	if err := del(pathID(r)); err != nil {
		if !isFK(err) {
			s.fail(w, "delete "+what, err)
			return
		}
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.done(w, r, back, "Data Deleted Successfully", what+".delete", name)
}
