package web

// Read-only list of isolations (isolir), one row per isolation record, with CSV export.

import (
	"encoding/csv"
	"net/http"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// isoState is the status column: running, due (past its end but not closed by the expiry job) or ended.
func isoState(endedAt, endsAt, now int64) string {
	switch {
	case endedAt > 0:
		return "ended"
	case endsAt <= now:
		return "due"
	}
	return "running"
}

// isoQuery reads the filter shared by the list and the CSV export; paging is added by the caller.
func isoQuery(r *http.Request) db.FilterIsolationsParams {
	g := r.URL.Query().Get
	q, _ := paging(r)
	status := g("status")
	if !oneOf(status, "running", "ended") {
		status = ""
	}
	return db.FilterIsolationsParams{Q: q, Status: status}
}

func (s *Server) isoList(w http.ResponseWriter, r *http.Request) {
	_, pg := paging(r)
	p := isoQuery(r)
	p.PageLimit, p.PageOffset = pg.limit(), pg.offset()
	rows, err := s.queries.FilterIsolations(r.Context(), p)
	if err != nil {
		s.fail(w, "list isolations", err)
		return
	}
	lp := listPage{Heading: "Isolations", Base: "/admin/isolations", Q: p.Q, Searchable: true,
		Cols:    []string{"Username", "Name", "Started On", "Ends On", "Ended On", "Reason", "Status"},
		Filters: []filter{{"status", p.Status, []option{{"", "Status"}, {"running", "running"}, {"ended", "ended"}}}}}
	lp.Links = []option{{"/admin/isolations/export?" + lp.query().Encode(), "Export CSV"}}
	now := time.Now().Unix()
	for _, x := range rows {
		ended := ""
		if x.EndedAt > 0 {
			ended = s.ts(x.EndedAt)
		}
		lp.Rows = append(lp.Rows, listRow{x.ID, []string{x.Username, x.Fullname, s.ts(x.StartedAt), s.ts(x.EndsAt), ended, x.EndReason, isoState(x.EndedAt, x.EndsAt, now)}})
	}
	total, err := s.queries.CountFilteredIsolations(r.Context(), db.CountFilteredIsolationsParams{Q: p.Q, Status: p.Status})
	if err != nil {
		s.fail(w, "count isolations", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

// isoExport streams the current filter (all pages) as CSV, like subExport.
func (s *Server) isoExport(w http.ResponseWriter, r *http.Request) {
	p := isoQuery(r)
	p.PageLimit = -1
	rows, err := s.queries.FilterIsolations(r.Context(), p)
	if err != nil {
		s.fail(w, "export isolations", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="isolations-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Username", "Name", "Started On", "Ends On", "Ended On", "Reason", "Status"})
	now := time.Now().Unix()
	for _, x := range rows {
		ended := ""
		if x.EndedAt > 0 {
			ended = s.ts(x.EndedAt)
		}
		rec := []string{x.Username, x.Fullname, s.ts(x.StartedAt), s.ts(x.EndsAt), ended, x.EndReason, isoState(x.EndedAt, x.EndsAt, now)}
		csvSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}
