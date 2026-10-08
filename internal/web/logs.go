package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func (s *Server) logList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchActivityLogs(r.Context(), db.SearchActivityLogsParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list logs", err)
		return
	}
	lp := listPage{Heading: "Logs", Base: "/admin/logs", Q: q, Searchable: true,
		Cols:  []string{"Date", "Actor", "Action", "Description", "IP"},
		Clean: "/admin/logs/clean/activity", Links: []option{{"/admin/logs/radius", "Radius Logs"}, {"/admin/logs/messages", "Message Logs"}}}
	for _, l := range rows {
		// ponytail: times shown in UTC; apply the timezone setting if operators ask
		when := time.Unix(l.CreatedAt, 0).UTC().Format("2006-01-02 15:04:05")
		lp.Rows = append(lp.Rows, listRow{l.ID, []string{when, fmt.Sprint(l.ActorType, " #", l.ActorID), l.Action, l.Description, l.Ip}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}
