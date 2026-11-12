package adminx

// Generic CRUD helpers: bulk delete, activity log and redirects with a flash.

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"
)

// BulkDelete runs del over the posted ids in ONE transaction, logs once and flashes "N Data Deleted Successfully".
// A foreign-key refusal rolls everything back and shows inUse.
func (s *Base) BulkDelete(w http.ResponseWriter, r *http.Request, back, what, inUse string, del func(q *db.Queries, ids []int64) (int64, error)) {
	ids, ok := httpx.ParseIDs(r)
	if !ok {
		s.Sessions.Put(r.Context(), "error", s.T("Select at least one row"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	tx, err := s.Conn.BeginTx(r.Context(), nil)
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
	s.BulkDone(w, r, back, what+".delete_many", n)
}

// BulkDone logs one activity entry with the count and flashes "N Data Deleted Successfully".
func (s *Base) BulkDone(w http.ResponseWriter, r *http.Request, back, action string, n int64) {
	s.LogActivity(r, action, fmt.Sprint(n))
	s.Sessions.Put(r.Context(), "flash", fmt.Sprintf("%d %s", n, s.T("Data Deleted Successfully")))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// LogActivity records an admin action. A failure is logged, never shown.
func (s *Base) LogActivity(r *http.Request, action, desc string) {
	err := s.Queries.CreateActivityLog(r.Context(), db.CreateActivityLogParams{
		ActorType: "admin", ActorID: core.AdminFrom(r).ID, Action: action, Description: desc, Ip: core.ClientIP(r),
	})
	if err != nil {
		slog.Error("activity log", "err", err)
	}
}

// Done flashes a message, logs the action and redirects.
func (s *Base) Done(w http.ResponseWriter, r *http.Request, back, msg, action, desc string) {
	s.LogActivity(r, action, desc)
	s.Sessions.Put(r.Context(), "flash", s.T(msg))
	if strings.Contains(action, "recharge") || strings.Contains(action, "deposit") { // money results stay until closed
		s.Sessions.Put(r.Context(), "flash_sticky", "1")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// Remove runs a delete; a foreign-key refusal becomes the friendly message inUse.
func (s *Base) Remove(w http.ResponseWriter, r *http.Request, back, what, inUse, name string, del func(id int64) error) {
	if err := del(httpx.PathID(r)); err != nil {
		if !httpx.IsFK(err) {
			s.Fail(w, "delete "+what, err)
			return
		}
		s.Sessions.Put(r.Context(), "error", s.T(inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.Done(w, r, back, "Data Deleted Successfully", what+".delete", name)
}
