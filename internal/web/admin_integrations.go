package web

// Integration checks: WhatsApp test and daily summary trigger.

import (
	"net/http"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/notify"
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
		return "Cannot reach the WA server. Check the URL and that the server is running"
	case we.Status == 401 || we.Status == 403:
		return "The WA server refused the username or password"
	case we.Status == 404:
		return "The WA server does not know this address. Check the WA server URL"
	}
	return "The WA server answered with an error"
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
