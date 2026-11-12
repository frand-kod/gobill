package customers

// Bulk message jobs: progress, status and selected-customer sending.

import (
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/core"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/httpx"

	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
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
func messageDelay(st map[string]string) time.Duration { return notify.MessageDelay(st) }

// runBulk sends to every customer in order in one goroutine, with the delay between messages.
func (s *Handlers) runBulk(st map[string]string, cs []db.Customer, channel, from, subject, tpl string) {
	defer bulk.finish()
	n, err := notify.Load(context.Background(), s.Queries)
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
		err := s.deliver(context.Background(), n, st, channel, from, subject, messageFor(n, c, st, tpl), c)
		bulk.record(c, err)
	}
}

func msgBulkFields(v, e map[string]string, routers []db.Router) []field {
	ro := []option{{Value: "", Label: "All Routers"}}
	for _, rt := range routers {
		ro = append(ro, option{Value: strconv.FormatInt(rt.ID, 10), Label: rt.Name})
	}
	return section([]field{
		{Name: "service", Label: "Service Type", Type: "select", Value: v["service"], Options: []option{{Value: "all", Label: "All"}, {Value: "PPPoE", Label: "PPPoE"}, {Value: "Hotspot", Label: "Hotspot"}, {Value: "VPN", Label: "VPN"}}},
		{Name: "router", Label: "Router", Type: "select", Value: v["router"], Options: ro},
		{Name: "status", Label: "Subscription Status", Type: "select", Value: v["status"], Options: []option{{Value: "all", Label: "All"}, {Value: "active", Label: "Active"}, {Value: "expired", Label: "Expired"}}},
		channelField(v, e),
		text("subject", "Subject", v, e).WithHint("Used for email and inbox"),
		{Name: "message", Label: "Message", Type: "textarea", Value: v["message"], Error: e["message"], Required: true, Hint: msgPlaceholders},
	}, "Bulk Message", "")
}

func (s *Handlers) msgBulkPage(w http.ResponseWriter, r *http.Request, code int, v, e map[string]string, errMsg string) {
	routers, err := s.Queries.ListRouters(r.Context(), db.ListRoutersParams{Limit: 500})
	if err != nil {
		s.Fail(w, "bulk routers", err)
		return
	}
	s.Render(w, r, code, "form", core.Page{Title: "Bulk Message", Error: errMsg,
		Data: formPage{Heading: "Bulk Message", Action: "/admin/message/bulk", Cancel: "/admin/message/bulk", Fields: msgBulkFields(v, e, routers)}})
}

func (s *Handlers) msgBulkForm(w http.ResponseWriter, r *http.Request) {
	s.msgBulkPage(w, r, 200, map[string]string{"service": "all", "status": "all", "channel": "sms"}, nil, "")
}

func (s *Handlers) msgBulkStart(w http.ResponseWriter, r *http.Request) {
	v := httpx.FormVals(r, "service", "router", "status", "channel", "subject", "message")
	e := map[string]string{}
	if !httpx.OneOf(v["service"], "all", "PPPoE", "Hotspot", "VPN") {
		e["service"] = "Choose a service type"
	}
	if v["router"] != "" {
		if _, ok := httpx.PosInt(v["router"]); !ok {
			e["router"] = "Choose a router"
		}
	}
	if !httpx.OneOf(v["status"], "all", "active", "expired") {
		e["status"] = "Choose a status"
	}
	if !httpx.OneOf(v["channel"], msgChannels...) {
		e["channel"] = "Choose a channel"
	}
	if v["message"] == "" {
		e["message"] = "Message is required"
	}
	if len(e) > 0 {
		s.msgBulkPage(w, r, 422, v, e, "")
		return
	}
	routerID, _ := httpx.PosInt(v["router"])
	svc, sub := v["service"], v["status"]
	if svc == "all" {
		svc = ""
	}
	if sub == "all" {
		sub = ""
	}
	cs, err := s.Queries.ListMessageRecipients(r.Context(), db.ListMessageRecipientsParams{ServiceType: svc, RouterID: routerID, SubStatus: sub})
	if err != nil {
		s.Fail(w, "bulk recipients", err)
		return
	}
	if len(cs) == 0 {
		s.msgBulkPage(w, r, 422, v, nil, "No customers match this filter")
		return
	}
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "bulk settings", err)
		return
	}
	if !bulk.start(len(cs)) {
		s.msgBulkPage(w, r, 409, v, nil, "A bulk message is already running")
		return
	}
	go s.runBulk(st, cs, v["channel"], adminx.AdminFrom(r).Username, v["subject"], v["message"])
	s.LogActivity(r, "message.bulk", fmt.Sprintf("%s to %d customers", v["channel"], len(cs)))
	http.Redirect(w, r, "/admin/message/bulk/status", http.StatusSeeOther)
}

// ---- bulk message to selected customers (list checkboxes) ----

func msgSelectedPage(v, e map[string]string) formPage {
	return formPage{Heading: "Send message to selected", Action: "/admin/message/selected", Cancel: "/admin/customers", Fields: section([]field{
		{Name: "ids", Type: "hidden", Value: v["ids"]},
		channelField(v, e),
		text("subject", "Subject", v, e).WithHint("Used for email and inbox"),
		{Name: "message", Label: "Message", Type: "textarea", Value: v["message"], Error: e["message"], Required: true, Hint: msgPlaceholders},
	}, "Bulk Message", "")}
}

// custMessageForm takes the ids ticked on the customer list and shows the compose form.
func (s *Handlers) custMessageForm(w http.ResponseWriter, r *http.Request) {
	ids, ok := httpx.ParseIDs(r)
	if !ok {
		s.Sessions.Put(r.Context(), "error", s.T("Select at least one row"))
		http.Redirect(w, r, "/admin/customers", http.StatusSeeOther)
		return
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = strconv.FormatInt(id, 10)
	}
	s.RenderForm(w, r, 200, msgSelectedPage(map[string]string{"ids": strings.Join(strs, ","), "channel": "sms"}, nil))
}

// msgSelectedSend starts the one bulk job for the chosen customers, then shows its progress page.
func (s *Handlers) msgSelectedSend(w http.ResponseWriter, r *http.Request) {
	v := httpx.FormVals(r, "ids", "channel", "subject", "message")
	e := map[string]string{}
	var ids []int64
	for _, f := range strings.Split(v["ids"], ",") {
		id, ok := httpx.PosInt(f)
		if !ok || len(ids) >= httpx.MaxBulkIDs {
			ids = nil
			break
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		s.Sessions.Put(r.Context(), "error", s.T("Select at least one row"))
		http.Redirect(w, r, "/admin/customers", http.StatusSeeOther)
		return
	}
	if !httpx.OneOf(v["channel"], msgChannels...) {
		e["channel"] = "Choose a channel"
	}
	if v["message"] == "" {
		e["message"] = "Message is required"
	}
	if len(e) > 0 {
		s.RenderForm(w, r, 422, msgSelectedPage(v, e))
		return
	}
	cs, err := s.Queries.ListCustomersByIDs(r.Context(), ids)
	if err != nil {
		s.Fail(w, "selected recipients", err)
		return
	}
	if len(cs) == 0 {
		s.Sessions.Put(r.Context(), "error", s.T("No customers match this filter"))
		http.Redirect(w, r, "/admin/customers", http.StatusSeeOther)
		return
	}
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "selected settings", err)
		return
	}
	if !bulk.start(len(cs)) {
		s.Sessions.Put(r.Context(), "error", s.T("A bulk message is already running"))
		http.Redirect(w, r, "/admin/message/bulk/status", http.StatusSeeOther)
		return
	}
	go s.runBulk(st, cs, v["channel"], adminx.AdminFrom(r).Username, v["subject"], v["message"])
	s.LogActivity(r, "message.bulk", fmt.Sprintf("%s to %d selected customers", v["channel"], len(cs)))
	http.Redirect(w, r, "/admin/message/bulk/status", http.StatusSeeOther)
}

func (s *Handlers) msgBulkStatus(w http.ResponseWriter, r *http.Request) {
	lp := listPage{Heading: "Bulk Message Status", Base: "/admin/message/bulk/status", Cols: []string{"Customer", "Phone", "Status"},
		Links: []option{{Value: "/admin/message/bulk/status", Label: "Refresh"}, {Value: "/admin/message/bulk", Label: "New Bulk Message"}}}
	bulk.mu.Lock()
	state := "Finished"
	if bulk.active {
		state = "Running"
	}
	lp.Rows = append(lp.Rows, listRow{ID: 0, Cells: []string{"Progress", fmt.Sprintf("%d of %d", bulk.done, bulk.total),
		fmt.Sprintf("%s: %d sent, %d failed", state, bulk.sent, bulk.failed)}})
	for _, row := range bulk.rows {
		lp.Rows = append(lp.Rows, listRow{ID: 0, Cells: []string{row.name, row.phone, row.status}})
	}
	bulk.mu.Unlock()
	s.RenderList(w, r, lp)
}
