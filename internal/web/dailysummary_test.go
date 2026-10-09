package web

import (
	"net/url"
	"strings"
	"testing"
)

func TestDailySummarySendNow(t *testing.T) {
	_, h, q := settingsSetup(t)
	for _, u := range []string{"alice", "bob"} { // SuperAdmin and Admin
		w := do(h, "POST", "/admin/settings/notifications/daily-summary", url.Values{}, login(t, h, u))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Not sent. Choose a channel") {
			t.Fatalf("%s: %d %s", u, w.Code, w.Body.String())
		}
	}
	if w := do(h, "POST", "/admin/settings/notifications/daily-summary", url.Values{}, nil); w.Code == 200 {
		t.Fatal("anonymous allowed")
	}
	// the form fields save, bad values are refused, the button is not a setting
	c := login(t, h, "alice")
	do(h, "POST", "/admin/settings/notifications", url.Values{"daily_summary_enabled": {"yes"}, "daily_summary_time": {"06:45"}, "daily_summary_channel": {"both"}, "reminder_hour": {"7"}}, c)
	v := settingValues(t, q)
	if v["daily_summary_time"] != "06:45" || v["daily_summary_channel"] != "both" || v["daily_summary_now"] != "" {
		t.Fatalf("%v", v)
	}
	w := do(h, "POST", "/admin/settings/notifications", url.Values{"daily_summary_time": {"25:99"}, "reminder_hour": {"7"}}, c)
	if !strings.Contains(w.Body.String(), "Use a time like 07:00") {
		t.Fatal("bad time accepted")
	}
}
