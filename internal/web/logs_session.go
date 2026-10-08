package web

import (
	"encoding/csv"
	"net/http"
	"strings"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

// dateRange reads ?from and ?to (YYYY-MM-DD, billing zone); to is inclusive. Unix 0 = open end.
func (s *Server) dateRange(r *http.Request) (from, to string, fromTS, toTS int64) {
	parse := func(k string) (string, time.Time, bool) {
		v := r.URL.Query().Get(k)
		t, err := time.ParseInLocation("2006-01-02", v, s.location())
		return v, t, err == nil
	}
	if v, t, ok := parse("from"); ok {
		from, fromTS = v, t.Unix()
	}
	if v, t, ok := parse("to"); ok {
		to, toTS = v, t.AddDate(0, 0, 1).Unix()
	}
	return
}

func (s *Server) writeCSV(w http.ResponseWriter, name string, head []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write(head)
	for _, rec := range rows {
		csvSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}

var radiusLogCols = []string{"User", "NAS", "IP", "MAC", "Start", "Stop", "Duration", "Upload", "Download"}

func (s *Server) radiusLogParams(r *http.Request) db.SearchRadiusLogsParams {
	_, _, f, t := s.dateRange(r)
	return db.SearchRadiusLogsParams{Q: strings.TrimSpace(r.URL.Query().Get("q")), FromTs: f, ToTs: t}
}

func (s *Server) radiusLogRows(ss []db.RadiusSession) [][]string {
	now, out := time.Now().Unix(), [][]string{}
	for _, x := range ss {
		stop, end := "", now
		if x.StoppedAt.Valid {
			stop, end = s.ts(x.StoppedAt.Int64), x.StoppedAt.Int64
		}
		out = append(out, []string{x.Username, x.NasIp, x.FramedIp, x.Mac, s.ts(x.StartedAt), stop, humanDur(end - x.StartedAt),
			humanBytes(x.InputOctets), humanBytes(x.OutputOctets)})
	}
	return out
}

// radiusLog lists all RADIUS sessions, open and closed (old logs/radius).
func (s *Server) radiusLog(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	p := s.radiusLogParams(r)
	p.PageLimit, p.PageOffset = limit, off
	ss, err := s.queries.SearchRadiusLogs(r.Context(), p)
	if err != nil {
		s.fail(w, "radius log", err)
		return
	}
	from, to, _, _ := s.dateRange(r)
	lp := listPage{Heading: "Radius Logs", Base: "/admin/logs/radius", Q: q, Searchable: true, Dates: true, From: from, To: to, Cols: radiusLogCols}
	for i, c := range s.radiusLogRows(ss) {
		lp.Rows = append(lp.Rows, listRow{ss[i].ID, c})
	}
	lp.Links = []option{{"/admin/logs/radius/export?" + lp.query().Encode(), "Export CSV"}}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) radiusLogExport(w http.ResponseWriter, r *http.Request) {
	p := s.radiusLogParams(r)
	p.PageLimit = -1
	ss, err := s.queries.SearchRadiusLogs(r.Context(), p)
	if err != nil {
		s.fail(w, "export radius log", err)
		return
	}
	s.writeCSV(w, "radius-logs", radiusLogCols, s.radiusLogRows(ss))
}

var msgLogCols = []string{"Date", "Type", "Recipient", "Subject", "Status", "Message"}

func (s *Server) msgLogParams(r *http.Request) db.SearchMessageLogsParams {
	_, _, f, t := s.dateRange(r)
	return db.SearchMessageLogsParams{Q: strings.TrimSpace(r.URL.Query().Get("q")), FromTs: f, ToTs: t}
}

func (s *Server) msgLogRows(ms []db.MessageLog) [][]string {
	out := [][]string{}
	for _, m := range ms {
		body := m.Body
		if m.Error != "" {
			body = m.Error + " | " + body
		}
		out = append(out, []string{s.ts(m.CreatedAt), m.Channel, m.Recipient, m.Subject, m.Status, body})
	}
	return out
}

// msgLog lists every send attempt of internal/notify (old logs/message).
func (s *Server) msgLog(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	p := s.msgLogParams(r)
	p.PageLimit, p.PageOffset = limit, off
	ms, err := s.queries.SearchMessageLogs(r.Context(), p)
	if err != nil {
		s.fail(w, "message log", err)
		return
	}
	from, to, _, _ := s.dateRange(r)
	lp := listPage{Heading: "Message Logs", Base: "/admin/logs/messages", Q: q, Searchable: true, Dates: true, From: from, To: to, Cols: msgLogCols}
	for i, c := range s.msgLogRows(ms) {
		lp.Rows = append(lp.Rows, listRow{ms[i].ID, c})
	}
	lp.Links = []option{{"/admin/logs/messages/export?" + lp.query().Encode(), "Export CSV"}}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) msgLogExport(w http.ResponseWriter, r *http.Request) {
	p := s.msgLogParams(r)
	p.PageLimit = -1
	ms, err := s.queries.SearchMessageLogs(r.Context(), p)
	if err != nil {
		s.fail(w, "export message log", err)
		return
	}
	s.writeCSV(w, "message-logs", msgLogCols, s.msgLogRows(ms))
}
