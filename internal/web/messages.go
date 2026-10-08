package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/notify"
)

// Old message.php (send, send_bulk) and mail.php (customer inbox). Channels: sms, wa, email, inbox.
// Staff only: Report cannot send, same as the old user_type list.

var msgChannels = []string{"sms", "wa", "email", "inbox"}

const msgPlaceholders = "Placeholders: [[name]], [[user_name]], [[phone]], [[company_name]]"

func channelField(v, e map[string]string) field {
	return field{Name: "channel", Label: "Send Via", Type: "select", Value: v["channel"], Error: e["channel"], Required: true,
		Options: []option{{"sms", "SMS"}, {"wa", "WhatsApp"}, {"email", "Email"}, {"inbox", "Inbox"}}}
}

// messageFor fills the old placeholders (str_replace in message.php).
func messageFor(c db.Customer, company, tpl string) string {
	return notify.Render(tpl, map[string]string{"name": c.Fullname, "user_name": c.Username, "phone": c.Phone, "company_name": company})
}

// deliver sends one message on one channel. Gateways that are not configured are an error, not a silent no-op.
func (s *Server) deliver(ctx context.Context, n *notify.Notifier, st map[string]string, channel, from, subject, text string, c db.Customer) error {
	if subject == "" {
		subject = "Notification Message"
	}
	ctx, cancel := context.WithTimeout(ctx, n.Timeout)
	defer cancel()
	switch channel {
	case "inbox":
		return s.queries.CreateInboxMessage(ctx, db.CreateInboxMessageParams{CustomerID: c.ID, FromName: from, Subject: subject, Body: text})
	case "sms", "wa":
		if st[channel+"_url"] == "" {
			return errors.New("gateway not configured")
		}
		if c.Phone == "" {
			return errors.New("no phone number")
		}
		if channel == "sms" {
			return n.SMS(ctx, c.Phone, text)
		}
		return n.WhatsApp(ctx, c.Phone, text)
	case "email":
		if st["smtp_host"] == "" {
			return errors.New("SMTP not configured")
		}
		if c.Email == "" {
			return errors.New("no email address")
		}
		return n.Email(ctx, c.Email, subject, text)
	}
	return errors.New("unknown channel")
}

// ---- single message ----

func msgSendFields(v, e map[string]string, customers []db.Customer) []field {
	co := []option{{"", "Select a customer"}}
	for _, c := range customers {
		co = append(co, option{strconv.FormatInt(c.ID, 10), c.Username + " - " + c.Fullname})
	}
	return section([]field{
		{Name: "customer_id", Label: "Customer", Type: "select", Value: v["customer_id"], Error: e["customer_id"], Required: true, Options: co},
		channelField(v, e),
		text("subject", "Subject", v, e).hint("Used for email and inbox"),
		{Name: "message", Label: "Message", Type: "textarea", Value: v["message"], Error: e["message"], Required: true, Hint: msgPlaceholders},
	}, "Message", "")
}

func (s *Server) msgSendPage(w http.ResponseWriter, r *http.Request, code int, v, e map[string]string, flash, errMsg string) {
	// ponytail: customer picker lists the newest 500 only; add a search box if the base grows past that.
	customers, err := s.queries.ListCustomers(r.Context(), db.ListCustomersParams{Limit: 500})
	if err != nil {
		s.fail(w, "message customers", err)
		return
	}
	s.render(w, r, code, "form", Page{Title: "Send Message", Flash: flash, Error: errMsg,
		Data: formPage{"Send Message", "/admin/message/send", "/admin/message/send", msgSendFields(v, e, customers)}})
}

func (s *Server) msgSendForm(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"channel": "sms", "customer_id": r.URL.Query().Get("customer")}
	s.msgSendPage(w, r, 200, v, nil, "", "")
}

func (s *Server) msgSend(w http.ResponseWriter, r *http.Request) {
	v := formVals(r, "customer_id", "channel", "subject", "message")
	e := map[string]string{}
	if _, ok := posInt(v["customer_id"]); !ok {
		e["customer_id"] = "Customer is required"
	}
	if !oneOf(v["channel"], msgChannels...) {
		e["channel"] = "Choose a channel"
	}
	if v["message"] == "" {
		e["message"] = "Message is required"
	}
	if len(e) > 0 {
		s.msgSendPage(w, r, 422, v, e, "", "")
		return
	}
	id, _ := posInt(v["customer_id"])
	c, err := s.queries.GetCustomer(r.Context(), id)
	if err == sql.ErrNoRows {
		s.msgSendPage(w, r, 422, v, map[string]string{"customer_id": "Customer not found"}, "", "")
		return
	} else if err != nil {
		s.fail(w, "message customer", err)
		return
	}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "message settings", err)
		return
	}
	n, err := notify.Load(r.Context(), s.queries)
	if err != nil {
		s.fail(w, "message notifier", err)
		return
	}
	err = s.deliver(r.Context(), n, st, v["channel"], adminFrom(r).Username, v["subject"], messageFor(c, st["company_name"], v["message"]), c)
	s.logActivity(r, "message.send", v["channel"]+" to "+c.Username)
	if err != nil {
		s.msgSendPage(w, r, 200, v, nil, "", "Failed to send message: "+err.Error())
		return
	}
	s.msgSendPage(w, r, 200, v, nil, s.catalog.T(s.language(), "Message Sent Successfully"), "")
}

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

// messageRoutes registers the admin side; staff is the SuperAdmin/Admin/Agent/Sales middleware.
func (s *Server) messageRoutes(mux *http.ServeMux, staff func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/message/send", staff(http.HandlerFunc(s.msgSendForm)))
	mux.Handle("POST /admin/message/send", staff(http.HandlerFunc(s.msgSend)))
	mux.Handle("GET /admin/message/bulk", staff(http.HandlerFunc(s.msgBulkForm)))
	mux.Handle("POST /admin/message/bulk", staff(http.HandlerFunc(s.msgBulkStart)))
	mux.Handle("GET /admin/message/bulk/status", staff(http.HandlerFunc(s.msgBulkStatus)))
}

// ---- customer inbox (old mail.php) ----

type inboxData struct {
	List []db.CustomersInbox
	View *db.CustomersInbox
}

func (s *Server) inboxRoutes(mux *http.ServeMux) {
	mux.Handle("GET /portal/inbox", s.requireCustomer(s.pInbox))
	mux.Handle("GET /portal/inbox/{id}", s.requireCustomer(s.pInboxView))
}

func (s *Server) pInbox(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListInboxByCustomer(r.Context(), db.ListInboxByCustomerParams{CustomerID: customerFrom(r).ID, Limit: 100})
	if err != nil {
		s.fail(w, "portal inbox", err)
		return
	}
	s.prender(w, r, 200, "p_inbox", Page{Title: "Inbox", Data: inboxData{List: rows}})
}

// pInboxView shows one message of this customer and marks it read.
func (s *Server) pInboxView(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	m, err := s.queries.GetInboxMessage(r.Context(), db.GetInboxMessageParams{ID: pathID(r), CustomerID: c.ID})
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "portal inbox view", err)
		return
	}
	if err := s.queries.MarkInboxRead(r.Context(), db.MarkInboxReadParams{ID: m.ID, CustomerID: c.ID}); err != nil {
		s.fail(w, "portal inbox read", err)
		return
	}
	s.prender(w, r, 200, "p_inbox", Page{Title: "Inbox", Data: inboxData{View: &m}})
}
