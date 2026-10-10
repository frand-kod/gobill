package web

// Integration checks: WhatsApp test and daily summary trigger.

import (
	"net/http"

	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/payment"
	"strconv"
	"strings"
	"time"
)

// waTest sends a test message with the (possibly unsaved) values of the WA server fields and
// shows the server's answer on the integrations page.
func (s *Server) waTest(w http.ResponseWriter, r *http.Request) {
	if adminFrom(r).Role != "SuperAdmin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "load settings", err)
		return
	}
	n, err := notify.Load(r.Context(), s.queries)
	if err != nil {
		s.fail(w, "wa test notifier", err)
		return
	}
	v := formVals(r, "alt_wga_server_url", "alt_wga_device_id", "alt_wga_username", "alt_wga_password", "wa_test_phone")
	for k, x := range v {
		if k != "wa_test_phone" && (k != "alt_wga_password" || x != "") {
			n.Settings[k] = x
		}
	}
	delete(v, "alt_wga_password")
	for k, x := range st { // fields not on this form keep their stored value
		if _, ok := v[k]; !ok {
			v[k] = x
		}
	}
	lang, e := s.language(), map[string]string{}
	switch {
	case n.Settings["alt_wga_server_url"] == "":
		e["wa_test_phone"] = "Fill in the WA server URL first"
	case v["wa_test_phone"] == "":
		e["wa_test_phone"] = "Enter a phone number for the test"
	default:
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		err := n.AltWA(ctx, v["wa_test_phone"], s.catalog.T(lang, "Test message from gobill. WhatsApp works."))
		s.logActivity(r, "wa.test", "ok="+strconv.FormatBool(err == nil))
		if err == nil {
			s.sessions.Put(r.Context(), "flash", s.catalog.T(lang, "Test message sent. Check the phone to be sure it arrived."))
		} else {
			msg := s.catalog.T(lang, waTestError(err))
			var we *notify.WAError
			if errors.As(err, &we) && we.Status > 0 && we.Status != 401 && we.Status != 403 && we.Status != 404 && we.Detail != "" {
				msg += ": " + we.Detail // the server's own words
			}
			e["wa_test_phone"] = msg
		}
	}
	s.renderSettings(w, r, http.StatusOK, "integrations", v, e)
}

// waTestError turns a send error into a plain sentence (Indonesian through the catalog).
func waTestError(err error) string {
	var we *notify.WAError
	if !errors.As(err, &we) {
		return err.Error()
	}
	switch {
	case we.Status == 0 && we.Detail == "invalid phone number":
		return "Invalid phone number. Use 10 to 15 digits, e.g. 08123456789"
	case we.Status == 0:
		return "Cannot reach GOWA. Check the URL and that the server is running"
	case we.Status == 401 || we.Status == 403:
		return "GOWA refused the username or password"
	case we.Status == 404:
		return "GOWA does not know this address. Check the GOWA URL"
	}
	return "GOWA answered with an error"
}

// integrationTest sends one short test through a channel of the Integrations tab, using the SAVED
// settings. The target is the operator, so notify_customers does not apply; the other switches
// and the channel's own checks still do. Result: a flash, or an error toast with the scrubbed text.
func (s *Server) integrationTest(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !oneOf(kind, "telegram", "gateway", "email", "webhook") {
		http.NotFound(w, r)
		return
	}
	if !s.testAllow(adminFrom(r).ID) {
		s.testResult(w, r, kind, "", errTestLimit)
		return
	}
	n, err := notify.Load(r.Context(), s.queries)
	if err != nil {
		s.fail(w, "integration test notifier", err)
		return
	}
	if s.telegramAPI != "" {
		n.TelegramAPI = s.telegramAPI
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	lang := s.language()
	text := s.catalog.T(lang, "Test message from gobill. The channel works.")
	var ok string
	switch kind {
	case "telegram":
		if n.Settings["telegram_bot"] == "" || n.Settings["telegram_target_id"] == "" {
			err = errTestNotSet
			break
		}
		err = n.Telegram(ctx, text)
		ok = s.catalog.T(lang, "Telegram test message sent. Check the chat to be sure it arrived.")
	case "gateway":
		to := r.PostFormValue("gateway_test_phone")
		switch {
		case !notify.SMSConfigured(n.Settings):
			err = errTestNotSet
		case to == "":
			err = errors.New(s.catalog.T(lang, "Enter a phone number for the test"))
		default:
			err = n.SMS(ctx, to, text)
			ok = s.catalog.T(lang, "Gateway test sent. Check the phone to be sure it arrived.")
		}
	case "email":
		to := r.PostFormValue("email_test_to")
		switch {
		case n.Settings["smtp_host"] == "":
			err = errTestNotSet
		case to == "":
			err = errors.New(s.catalog.T(lang, "Enter an email address for the test"))
		default:
			err = n.Email(ctx, to, s.catalog.T(lang, "Test email from gobill"), text)
			ok = s.catalog.T(lang, "Test email sent. Check the inbox to be sure it arrived.")
		}
	case "webhook":
		if n.Settings["webhook_url"] == "" {
			err = errTestNotSet
			break
		}
		var status int
		status, err = n.PostWebhook(ctx, "test", map[string]any{})
		ok = fmt.Sprintf(s.catalog.T(lang, "Webhook answered HTTP %d"), status)
	}
	s.testResult(w, r, kind, ok, err)
}

// tripayTest checks the saved Tripay keys with the payment channel list, the cheapest authenticated call.
func (s *Server) tripayTest(w http.ResponseWriter, r *http.Request) {
	if !s.testAllow(adminFrom(r).ID) {
		s.testResult(w, r, "tripay", "", errTestLimit)
		return
	}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "load settings", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var ok string
	g, err := s.gateway(st)
	switch {
	case err != nil:
	case g == nil:
		err = errors.New(s.catalog.T(s.language(), "Choose Tripay as the Payment Gateway and save first"))
	default:
		var chans []payment.Channel
		if chans, err = g.Channels(ctx); err == nil {
			mode := st["tripay_mode"]
			if mode != "production" {
				mode = "sandbox"
			}
			ok = fmt.Sprintf(s.catalog.T(s.language(), "Tripay connection OK (%s mode, %d payment channels)"), mode, len(chans))
		}
	}
	s.testResult(w, r, "tripay", ok, err)
}

var (
	errTestNotSet = errors.New("Fill in the settings of this channel and save first")
	errTestLimit  = errors.New("Too many tests. Wait a minute and try again")
)

// testAllow lets an admin run 5 integration tests a minute, so the buttons cannot be used to spam.
func (s *Server) testAllow(admin int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.testSent == nil {
		s.testSent = map[int64][]time.Time{}
	}
	now := time.Now()
	var recent []time.Time
	for _, t := range s.testSent[admin] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 5 {
		s.testSent[admin] = recent
		return false
	}
	s.testSent[admin] = append(recent, now)
	return true
}

// testResult logs the test and redirects with the outcome: a flash on success, an error toast otherwise.
// err is the test's own message or a server error text, scrubbed of URLs, tokens and numbers.
func (s *Server) testResult(w http.ResponseWriter, r *http.Request, kind, ok string, err error) {
	back, lang := "/admin/settings/integrations", s.language()
	if kind == "tripay" {
		back = "/admin/settings/payment"
	}
	s.logActivity(r, "integration.test", kind+" ok="+strconv.FormatBool(err == nil))
	if err == nil {
		s.sessions.Put(r.Context(), "flash", ok)
	} else {
		s.sessions.Put(r.Context(), "error", s.catalog.T(lang, "Test failed")+": "+s.catalog.T(lang, notify.Scrub(err.Error())))
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// dailySummaryNow sends the summary immediately from the saved settings and shows a plain result.
func (s *Server) dailySummaryNow(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "load settings", err)
		return
	}
	lang, e := s.language(), map[string]string{}
	var sent []string
	if s.Billing != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		sent, err = s.Billing.SendDailySummary(ctx)
	} else {
		err = billing.ErrSummaryNotConfigured
	}
	s.logActivity(r, "daily_summary.send", "ok="+strconv.FormatBool(err == nil))
	switch {
	case err == nil:
		s.sessions.Put(r.Context(), "flash", s.catalog.T(lang, "Summary sent")+": "+strings.Join(sent, ", "))
	case errors.Is(err, billing.ErrSummaryNotConfigured):
		e["daily_summary_now"] = s.catalog.T(lang, "Not sent. Choose a channel and fill in the Telegram ID or the WhatsApp number and server first")
	default:
		e["daily_summary_now"] = s.catalog.T(lang, "Could not send the summary") + ": " + err.Error()
	}
	s.renderSettings(w, r, http.StatusOK, "notifications", st, e)
}
