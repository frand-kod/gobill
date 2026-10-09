package web

// Bulk message jobs: progress, status and selected-customer sending.

import (
	"net/http"

	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- bulk message ----

// bulkJob is the progress of the one bulk run. ponytail: in-memory, resets on restart; store in a table if it must survive.
type bulkJob struct {
	mu                        sync.Mutex
	active                    bool
	total, done, sent, failed int
	rows                      []bulkRow
}

type bulkRow struct{ name, phone, status string }

var bulk bulkJob

func (j *bulkJob) start(total int) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.active {
		return false
	}
	j.active, j.total, j.done, j.sent, j.failed, j.rows = true, total, 0, 0, 0, nil
	return true
}

func (j *bulkJob) record(c db.Customer, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.done++
	status := "Sent"
	if err != nil {
		j.failed++
		status = "Failed: " + err.Error()
	} else {
		j.sent++
	}
	j.rows = append(j.rows, bulkRow{c.Fullname, c.Phone, status})
}

func (j *bulkJob) finish() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.active = false
}

// messageDelay is the pause between bulk messages: setting message_delay in seconds, default 1.
func messageDelay(st map[string]string) time.Duration {
	sec, err := strconv.ParseFloat(st["message_delay"], 64)
	if err != nil || sec < 0 {
		sec = 1
	}
	return time.Duration(sec * float64(time.Second))
}

// runBulk sends to every customer in order in one goroutine, with the delay between messages.
func (s *Server) runBulk(st map[string]string, cs []db.Customer, channel, from, subject, tpl string) {
	defer bulk.finish()
	n, err := notify.Load(context.Background(), s.queries)
	if err != nil {
		for _, c := range cs {
			bulk.record(c, err)
		}
		return
	}
	delay := messageDelay(st)
	for i, c := range cs {
		if i > 0 && delay > 0 {
			time.Sleep(delay)
		}
		err := s.deliver(context.Background(), n, st, channel, from, subject, messageFor(c, st["company_name"], tpl), c)
		bulk.record(c, err)
	}
}

func msgBulkFields(v, e map[string]string, routers []db.Router) []field {
	ro := []option{{"", "All Routers"}}
	for _, rt := range routers {
		ro = append(ro, option{strconv.FormatInt(rt.ID, 10), rt.Name})
	}
	return section([]field{
		{Name: "service", Label: "Service Type", Type: "select", Value: v["service"], Options: []option{{"all", "All"}, {"PPPoE", "PPPoE"}, {"Hotspot", "Hotspot"}, {"VPN", "VPN"}}},
		{Name: "router", Label: "Router", Type: "select", Value: v["router"], Options: ro},
		{Name: "status", Label: "Subscription Status", Type: "select", Value: v["status"], Options: []option{{"all", "All"}, {"active", "Active"}, {"expired", "Expired"}}},
		channelField(v, e),
		text("subject", "Subject", v, e).hint("Used for email and inbox"),
		{Name: "message", Label: "Message", Type: "textarea", Value: v["message"], Error: e["message"], Required: true, Hint: msgPlaceholders},
	}, "Bulk Message", "")
}

func (s *Server) msgBulkPage(w http.ResponseWriter, r *http.Request, code int, v, e map[string]string, errMsg string) {
	routers, err := s.queries.ListRouters(r.Context(), db.ListRoutersParams{Limit: 500})
	if err != nil {
		s.fail(w, "bulk routers", err)
		return
	}
	s.render(w, r, code, "form", Page{Title: "Bulk Message", Error: errMsg,
		Data: formPage{"Bulk Message", "/admin/message/bulk", "/admin/message/bulk", msgBulkFields(v, e, routers)}})
}

func (s *Server) msgBulkForm(w http.ResponseWriter, r *http.Request) {
	s.msgBulkPage(w, r, 200, map[string]string{"service": "all", "status": "all", "channel": "sms"}, nil, "")
}

func (s *Server) msgBulkStart(w http.ResponseWriter, r *http.Request) {
	v := formVals(r, "service", "router", "status", "channel", "subject", "message")
	e := map[string]string{}
	if !oneOf(v["service"], "all", "PPPoE", "Hotspot", "VPN") {
		e["service"] = "Choose a service type"
	}
	if v["router"] != "" {
		if _, ok := posInt(v["router"]); !ok {
			e["router"] = "Choose a router"
		}
	}
	if !oneOf(v["status"], "all", "active", "expired") {
		e["status"] = "Choose a status"
	}
	if !oneOf(v["channel"], msgChannels...) {
		e["channel"] = "Choose a channel"
	}
	if v["message"] == "" {
		e["message"] = "Message is required"
	}
	if len(e) > 0 {
		s.msgBulkPage(w, r, 422, v, e, "")
		return
	}
	routerID, _ := posInt(v["router"])
	svc, sub := v["service"], v["status"]
	if svc == "all" {
		svc = ""
	}
	if sub == "all" {
		sub = ""
	}
	cs, err := s.queries.ListMessageRecipients(r.Context(), db.ListMessageRecipientsParams{ServiceType: svc, RouterID: routerID, SubStatus: sub})
	if err != nil {
		s.fail(w, "bulk recipients", err)
		return
	}
	if len(cs) == 0 {
		s.msgBulkPage(w, r, 422, v, nil, "No customers match this filter")
		return
	}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "bulk settings", err)
		return
	}
	if !bulk.start(len(cs)) {
		s.msgBulkPage(w, r, 409, v, nil, "A bulk message is already running")
		return
	}
	go s.runBulk(st, cs, v["channel"], adminFrom(r).Username, v["subject"], v["message"])
	s.logActivity(r, "message.bulk", fmt.Sprintf("%s to %d customers", v["channel"], len(cs)))
	http.Redirect(w, r, "/admin/message/bulk/status", http.StatusSeeOther)
}

// ---- bulk message to selected customers (list checkboxes) ----

func msgSelectedPage(v, e map[string]string) formPage {
	return formPage{"Send message to selected", "/admin/message/selected", "/admin/customers", section([]field{
		{Name: "ids", Type: "hidden", Value: v["ids"]},
		channelField(v, e),
		text("subject", "Subject", v, e).hint("Used for email and inbox"),
		{Name: "message", Label: "Message", Type: "textarea", Value: v["message"], Error: e["message"], Required: true, Hint: msgPlaceholders},
	}, "Bulk Message", "")}
}

// custMessageForm takes the ids ticked on the customer list and shows the compose form.
func (s *Server) custMessageForm(w http.ResponseWriter, r *http.Request) {
	ids, ok := parseIDs(r)
	if !ok {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Select at least one row"))
		http.Redirect(w, r, "/admin/customers", http.StatusSeeOther)
		return
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = strconv.FormatInt(id, 10)
	}
	s.renderForm(w, r, 200, msgSelectedPage(map[string]string{"ids": strings.Join(strs, ","), "channel": "sms"}, nil))
}

// msgSelectedSend starts the one bulk job for the chosen customers, then shows its progress page.
func (s *Server) msgSelectedSend(w http.ResponseWriter, r *http.Request) {
	v := formVals(r, "ids", "channel", "subject", "message")
	e := map[string]string{}
	var ids []int64
	for _, f := range strings.Split(v["ids"], ",") {
		id, ok := posInt(f)
		if !ok || len(ids) >= maxBulkIDs {
			ids = nil
			break
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Select at least one row"))
		http.Redirect(w, r, "/admin/customers", http.StatusSeeOther)
		return
	}
	if !oneOf(v["channel"], msgChannels...) {
		e["channel"] = "Choose a channel"
	}
	if v["message"] == "" {
		e["message"] = "Message is required"
	}
	if len(e) > 0 {
		s.renderForm(w, r, 422, msgSelectedPage(v, e))
		return
	}
	cs, err := s.queries.ListCustomersByIDs(r.Context(), ids)
	if err != nil {
		s.fail(w, "selected recipients", err)
		return
	}
	if len(cs) == 0 {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "No customers match this filter"))
		http.Redirect(w, r, "/admin/customers", http.StatusSeeOther)
		return
	}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "selected settings", err)
		return
	}
	if !bulk.start(len(cs)) {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "A bulk message is already running"))
		http.Redirect(w, r, "/admin/message/bulk/status", http.StatusSeeOther)
		return
	}
	go s.runBulk(st, cs, v["channel"], adminFrom(r).Username, v["subject"], v["message"])
	s.logActivity(r, "message.bulk", fmt.Sprintf("%s to %d selected customers", v["channel"], len(cs)))
	http.Redirect(w, r, "/admin/message/bulk/status", http.StatusSeeOther)
}

func (s *Server) msgBulkStatus(w http.ResponseWriter, r *http.Request) {
	lp := listPage{Heading: "Bulk Message Status", Base: "/admin/message/bulk/status", Cols: []string{"Customer", "Phone", "Status"},
		Links: []option{{"/admin/message/bulk/status", "Refresh"}, {"/admin/message/bulk", "New Bulk Message"}}}
	bulk.mu.Lock()
	state := "Finished"
	if bulk.active {
		state = "Running"
	}
	lp.Rows = append(lp.Rows, listRow{0, []string{"Progress", fmt.Sprintf("%d of %d", bulk.done, bulk.total),
		fmt.Sprintf("%s: %d sent, %d failed", state, bulk.sent, bulk.failed)}})
	for _, row := range bulk.rows {
		lp.Rows = append(lp.Rows, listRow{0, []string{row.name, row.phone, row.status}})
	}
	bulk.mu.Unlock()
	s.renderList(w, r, lp)
}
