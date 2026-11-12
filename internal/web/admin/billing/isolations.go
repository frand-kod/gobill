package billing

// Read-only list of isolations (isolir), one row per isolation record, with CSV export.

import (
	"encoding/csv"
	"net/http"
	"time"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

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
	q, _ := view.Paging(r)
	status := g("status")
	if !httpx.OneOf(status, "running", "ended") {
		status = ""
	}
	return db.FilterIsolationsParams{Q: q, Status: status}
}

func (s *Handlers) isoList(w http.ResponseWriter, r *http.Request) {
	_, pg := view.Paging(r)
	p := isoQuery(r)
	p.PageLimit, p.PageOffset = pg.Limit(), pg.Offset()
	rows, err := s.Queries.FilterIsolations(r.Context(), p)
	if err != nil {
		s.Fail(w, "list isolations", err)
		return
	}
	lp := listPage{Heading: "Isolations", Base: "/admin/isolations", Q: p.Q, Searchable: true,
		Cols:    []string{"Username", "Name", "Started On", "Ends On", "Ended On", "Reason", "Status"},
		Filters: []filter{{Name: "status", Val: p.Status, Opts: []option{{Value: "", Label: "Status"}, {Value: "running", Label: "running"}, {Value: "ended", Label: "ended"}}, Label: "Status"}}}
	lp.Links = []option{{Value: "/admin/isolations/export?" + lp.Query().Encode(), Label: "Export CSV"}}
	now := time.Now().Unix()
	for _, x := range rows {
		ended := ""
		if x.EndedAt > 0 {
			ended = s.TS(x.EndedAt)
		}
		lp.Rows = append(lp.Rows, listRow{ID: x.ID, Cells: []string{x.Username, x.Fullname, s.TS(x.StartedAt), s.TS(x.EndsAt), ended, x.EndReason, isoState(x.EndedAt, x.EndsAt, now)}})
	}
	total, err := s.Queries.CountFilteredIsolations(r.Context(), db.CountFilteredIsolationsParams{Q: p.Q, Status: p.Status})
	if err != nil {
		s.Fail(w, "count isolations", err)
		return
	}
	lp.Finish(pg, total)
	s.RenderList(w, r, lp)
}

// isoExport streams the current filter (all pages) as CSV, like subExport.
func (s *Handlers) isoExport(w http.ResponseWriter, r *http.Request) {
	p := isoQuery(r)
	p.PageLimit = -1
	rows, err := s.Queries.FilterIsolations(r.Context(), p)
	if err != nil {
		s.Fail(w, "export isolations", err)
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
			ended = s.TS(x.EndedAt)
		}
		rec := []string{x.Username, x.Fullname, s.TS(x.StartedAt), s.TS(x.EndsAt), ended, x.EndReason, isoState(x.EndedAt, x.EndsAt, now)}
		httpx.CSVSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}
