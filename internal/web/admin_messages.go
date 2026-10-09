package web

// Message sending to a customer or channel, with templates.

import (
	"database/sql"
	"net/http"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"strconv"
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
		if (channel == "sms" && st["sms_url"] == "") || (channel == "wa" && !notify.WAConfigured(st)) {
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
