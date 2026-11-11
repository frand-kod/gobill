package web

// Activity, RADIUS and message logs: views, CSV export and cleanup.

import (
	"context"
	"encoding/csv"
	"log/slog"
	"net/http"

	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"strings"
	"time"
)

// ---- log cleanup ----

var logPages = map[string]string{"activity": "/admin/logs", "radius": "/admin/logs/radius", "auth": "/admin/logs/auth", "messages": "/admin/logs/messages"}

func (s *Server) logClean(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	back, ok := logPages[kind]
	if !ok {
		http.NotFound(w, r)
		return
	}
	days, ok := posInt(strings.TrimSpace(r.PostFormValue("keep")))
	if !ok || days > 36500 || s.Billing == nil {
		s.sessions.Put(r.Context(), "error", s.t("Enter a number greater than 0"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	n, err := s.Billing.CleanLog(r.Context(), kind, int(days))
	if err != nil {
		s.fail(w, "clean logs", err)
		return
	}
	s.logActivity(r, "logs.clean", fmt.Sprintf("%s older than %d days: %d deleted", kind, days, n))
	s.sessions.Put(r.Context(), "flash", fmt.Sprintf(s.t("%d logs deleted"), n))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// actionLabels names the activity log actions (the key stored in activity_logs.action). Values are English
// keys translated by T; an action not listed here is shown as its key.
var actionLabels = map[string]string{
	"agent_batch.return":            "Agent batch returned",
	"coupon.toggle":                 "Coupon switched",
	"customer.delete":               "Customer deleted",
	"customer.recharge":             "Customer recharged",
	"customer.recharge.bulk":        "Bulk recharge",
	"customer.impersonate":          "Signed in as customer",
	"customer.delete_router_failed": "Customer removed, router removal failed",
	"customer.sync_failed":          "Customer sync failed",
	"daily_summary.send":            "Daily summary sent",
	"integration.test":              "Integration test",
	"logs.clean":                    "Logs cleaned",
	"message.bulk":                  "Bulk message sent",
	"message.send":                  "Message sent",
	"metrics.token_new":             "Metrics token created",
	"metrics.token_off":             "Metrics token turned off",
	"page.update":                   "Page updated",
	"plan.sync_failed":              "Plan sync failed",
	"pool.sync_failed":              "Pool sync failed",
	"promo.delete":                  "Promo deleted",
	"radius.disconnect":             "Session disconnected",
	"radius.disconnect_many":        "Sessions disconnected",
	"radius.test_login":             "Test login",
	"router.radius_disable_local":   "Router RADIUS disabled locally",
	"router.radius_rollback":        "Router RADIUS rolled back",
	"router.radius_switch":          "Router RADIUS switched",
	"router.radius_test":            "Router RADIUS tested",
	"router.reconcile":              "Router reconciled",
	"router.sync":                   "Router synced",
	"router.wg.provision":           "WireGuard provisioned",
	"router.wg.reset":               "WireGuard reset",
	"settings.key.download":         "Settings key downloaded",
	"settings.key.saved":            "Settings key saved",
	"settings.reset":                "Settings reset",
	"settings.save":                 "Settings saved",
	"theme.default":                 "Default theme changed",
	"users.create":                  "User created",
	"users.delete":                  "User deleted",
	"users.password":                "Password changed",
	"users.password.reset":          "Password reset",
	"users.update":                  "User updated",
	"voucher.generate":              "Vouchers generated",
	"wa.test":                       "WhatsApp test",
}

// actionLabel is the UI text of an activity log action key.
func (s *Server) actionLabel(action string) string {
	if l, ok := actionLabels[action]; ok {
		return s.t(l)
	}
	return action
}

// actorLabel names who did a logged action: the admin's username, or "type #id" when there is no such admin.
func (s *Server) actorLabel(ctx context.Context, typ string, id int64) string {
	if typ == "admin" {
		if a, err := s.queries.GetAdmin(ctx, id); err == nil {
			return a.Username
		}
	}
	return fmt.Sprint(typ, " #", id)
}

func (s *Server) logList(w http.ResponseWriter, r *http.Request) {
	q, pg := paging(r)
	rows, err := s.queries.SearchActivityLogs(r.Context(), db.SearchActivityLogsParams{Q: q, PageLimit: pg.limit(), PageOffset: pg.offset()})
	if err != nil {
		s.fail(w, "list logs", err)
		return
	}
	lp := listPage{Heading: "Logs", Base: "/admin/logs", Q: q, Searchable: true,
		Cols:  []string{"Date", "Actor", "Action", "Description", "IP"},
		Clean: "/admin/logs/clean/activity", Links: []option{{"/admin/logs/radius", "Radius Logs"}, {"/admin/logs/auth", "Login attempts"}, {"/admin/logs/messages", "Message Logs"}}}
	for _, l := range rows {
		when := time.Unix(l.CreatedAt, 0).In(s.location()).Format("2006-01-02 15:04:05")
		lp.Rows = append(lp.Rows, listRow{l.ID, []string{when, s.actorLabel(r.Context(), l.ActorType, l.ActorID), s.actionLabel(l.Action), l.Description, l.Ip}})
	}
	total, err := s.queries.CountActivityLogs(r.Context(), q)
	if err != nil {
		s.fail(w, "count logs", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

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

// exportChunk is how many log rows a CSV export reads at a time, so a long log is never held in memory whole.
const exportChunk = 1000

// exportCSV streams a log export: page(off) returns the rows from off (at most exportChunk). The first
// page is read before the headers go out, so a failure there can still be an error response; later
// failures can only be logged, and the file ends early.
func (s *Server) exportCSV(w http.ResponseWriter, name string, head []string, page func(off int64) ([][]string, error)) error {
	rows, err := page(0)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write(head)
	for off := int64(0); ; {
		for _, rec := range rows {
			csvSafe(rec)
			cw.Write(rec)
		}
		cw.Flush()
		if len(rows) < exportChunk {
			return nil
		}
		off += exportChunk
		if rows, err = page(off); err != nil {
			slog.Error("csv export stopped", "file", name, "err", err)
			return nil
		}
	}
}

var radiusLogCols = []string{"User", "NAS", "IP", "MAC", "Start", "Stop", "Duration", "Upload", "Download", "End reason"}

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
			humanBytes(x.InputOctets), humanBytes(x.OutputOctets), x.TerminateCause})
	}
	return out
}

// radiusLog lists all RADIUS sessions, open and closed (old logs/radius).
func (s *Server) radiusLog(w http.ResponseWriter, r *http.Request) {
	q, pg := paging(r)
	p := s.radiusLogParams(r)
	p.PageLimit, p.PageOffset = pg.limit(), pg.offset()
	ss, err := s.queries.SearchRadiusLogs(r.Context(), p)
	if err != nil {
		s.fail(w, "radius log", err)
		return
	}
	from, to, _, _ := s.dateRange(r)
	lp := listPage{Heading: "Radius Logs", Base: "/admin/logs/radius", Q: q, Searchable: true, Dates: true, From: from, To: to, Cols: radiusLogCols, Clean: "/admin/logs/clean/radius"}
	for i, c := range s.radiusLogRows(ss) {
		lp.Rows = append(lp.Rows, listRow{ss[i].ID, c})
	}
	lp.Links = []option{{"/admin/logs/radius/export?" + lp.query().Encode(), "Export CSV"}, {"/admin/logs/auth", "Login attempts"}, {"/admin/radius/ip-lookup", "Who had this IP"}}
	total, err := s.queries.CountRadiusLogs(r.Context(), db.CountRadiusLogsParams{Q: p.Q, FromTs: p.FromTs, ToTs: p.ToTs})
	if err != nil {
		s.fail(w, "count radius logs", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) radiusLogExport(w http.ResponseWriter, r *http.Request) {
	p := s.radiusLogParams(r)
	err := s.exportCSV(w, "radius-logs", radiusLogCols, func(off int64) ([][]string, error) {
		p.PageLimit, p.PageOffset = exportChunk, off
		ss, err := s.queries.SearchRadiusLogs(r.Context(), p)
		return s.radiusLogRows(ss), err
	})
	if err != nil {
		s.fail(w, "export radius log", err)
	}
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
	q, pg := paging(r)
	p := s.msgLogParams(r)
	p.PageLimit, p.PageOffset = pg.limit(), pg.offset()
	ms, err := s.queries.SearchMessageLogs(r.Context(), p)
	if err != nil {
		s.fail(w, "message log", err)
		return
	}
	from, to, _, _ := s.dateRange(r)
	lp := listPage{Heading: "Message Logs", Base: "/admin/logs/messages", Q: q, Searchable: true, Dates: true, From: from, To: to, Cols: msgLogCols, Clean: "/admin/logs/clean/messages"}
	for i, c := range s.msgLogRows(ms) {
		lp.Rows = append(lp.Rows, listRow{ms[i].ID, c})
	}
	lp.Links = []option{{"/admin/logs/messages/export?" + lp.query().Encode(), "Export CSV"}}
	total, err := s.queries.CountMessageLogs(r.Context(), db.CountMessageLogsParams{Q: p.Q, FromTs: p.FromTs, ToTs: p.ToTs})
	if err != nil {
		s.fail(w, "count message logs", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) msgLogExport(w http.ResponseWriter, r *http.Request) {
	p := s.msgLogParams(r)
	err := s.exportCSV(w, "message-logs", msgLogCols, func(off int64) ([][]string, error) {
		p.PageLimit, p.PageOffset = exportChunk, off
		ms, err := s.queries.SearchMessageLogs(r.Context(), p)
		return s.msgLogRows(ms), err
	})
	if err != nil {
		s.fail(w, "export message log", err)
	}
}

var authLogCols = []string{"Date", "User", "NAS", "MAC", "Result", "Reason"}

// authLogListCols is the page header: the column named "Status" makes list.html draw the result as a badge.
var authLogListCols = []string{"Date", "User", "NAS", "MAC", "Status", "Reason"}

func (s *Server) authLogParams(r *http.Request) db.SearchRadiusAuthLogParams {
	_, _, f, t := s.dateRange(r)
	return db.SearchRadiusAuthLogParams{Q: strings.TrimSpace(r.URL.Query().Get("q")), FromTs: f, ToTs: t}
}

// authLogRows: the export gets translated results and raw reasons. The page (page=true) gets the English
// result key, which list.html translates and badges, and the reason translated through rejectLabels.
func (s *Server) authLogRows(rows []db.RadiusAuthLog, page bool) [][]string {
	out := [][]string{}
	for _, x := range rows {
		res, reason := "Rejected", x.Message
		if x.Accepted == 1 {
			res = "Accepted"
		}
		if page {
			if k, ok := rejectLabels[x.Message]; ok {
				reason = s.t(k)
			}
		} else {
			res = s.t(res)
		}
		out = append(out, []string{s.ts(x.At), x.Username, x.NasIp, x.Mac, res, reason})
	}
	return out
}

// radiusAuthLog lists every RADIUS login attempt (post-auth log): accepted or rejected, with the reason.
func (s *Server) radiusAuthLog(w http.ResponseWriter, r *http.Request) {
	q, pg := paging(r)
	p := s.authLogParams(r)
	p.PageLimit, p.PageOffset = pg.limit(), pg.offset()
	rows, err := s.queries.SearchRadiusAuthLog(r.Context(), p)
	if err != nil {
		s.fail(w, "auth log", err)
		return
	}
	from, to, _, _ := s.dateRange(r)
	lp := listPage{Heading: "Login attempts", Base: "/admin/logs/auth", Q: q, Searchable: true, Dates: true, From: from, To: to, Cols: authLogListCols, Clean: "/admin/logs/clean/auth"}
	for i, c := range s.authLogRows(rows, true) {
		lp.Rows = append(lp.Rows, listRow{rows[i].ID, c})
	}
	lp.Links = []option{{"/admin/logs/auth/export?" + lp.query().Encode(), "Export CSV"}, {"/admin/logs/radius", "Radius Logs"}}
	total, err := s.queries.CountRadiusAuthLog(r.Context(), db.CountRadiusAuthLogParams{Q: p.Q, FromTs: p.FromTs, ToTs: p.ToTs})
	if err != nil {
		s.fail(w, "count auth log", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) radiusAuthLogExport(w http.ResponseWriter, r *http.Request) {
	p := s.authLogParams(r)
	err := s.exportCSV(w, "login-attempts", authLogCols, func(off int64) ([][]string, error) {
		p.PageLimit, p.PageOffset = exportChunk, off
		rows, err := s.queries.SearchRadiusAuthLog(r.Context(), p)
		return s.authLogRows(rows, false), err
	})
	if err != nil {
		s.fail(w, "export auth log", err)
	}
}
