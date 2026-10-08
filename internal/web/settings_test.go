package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

// settingsSetup adds an Admin (bob) next to the SuperAdmin alice and the Report rita.
func settingsSetup(t *testing.T) (*Server, http.Handler, *db.Queries) {
	s, q := newTestApp(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if _, err := q.CreateAdmin(t.Context(), db.CreateAdminParams{Username: "bob", Fullname: "Bob", PasswordHash: string(hash), Role: "Admin"}); err != nil {
		t.Fatal(err)
	}
	s.lang.Store("english") // assertions below match English messages
	return s, s.Handler(), q
}

func settingValues(t *testing.T, q *db.Queries) map[string]string {
	t.Helper()
	rows, err := q.ListSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m
}

func TestSettingsPagesRender(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	for _, tab := range []string{"app", "localisation", "notifications", "integrations", "miscellaneous"} {
		if w := do(h, "GET", "/admin/settings/"+tab, nil, c); w.Code != 200 {
			t.Fatalf("%s: %d", tab, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/settings", nil, c); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/settings/app" {
		t.Fatalf("bare settings: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/settings/nope", nil, c); w.Code != http.StatusNotFound {
		t.Fatalf("unknown tab: %d", w.Code)
	}
}

func TestSettingsSavePersists(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	form := url.Values{"company_name": {"Acme"}, "company_footer": {"Acme ISP"}, "address": {"Jl. Merdeka 1"},
		"phone": {"0812"}, "note": {"Thanks"}, "currency_code": {"Rp"}}
	w := do(h, "POST", "/admin/settings/app", form, c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/settings/app" {
		t.Fatalf("save: %d", w.Code)
	}
	if got := settingValues(t, q); got["company_name"] != "Acme" || got["address"] != "Jl. Merdeka 1" {
		t.Fatalf("not persisted: %v", got)
	}
	w = do(h, "POST", "/admin/settings/localisation", url.Values{"language": {"english"}, "timezone": {"Asia/Makassar"},
		"date_format": {"d-m-Y"}, "country_code_phone": {"62"}}, c)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("localisation save: %d", w.Code)
	}
	w = do(h, "GET", "/admin/settings/localisation", nil, c)
	if !strings.Contains(w.Body.String(), "Asia/Makassar") || !strings.Contains(w.Body.String(), "Settings saved") {
		t.Fatalf("value or flash missing: %s", w.Body.String())
	}
}

func TestSettingsSecretNotRenderedAndKept(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	do(h, "POST", "/admin/settings/integrations", url.Values{"telegram_bot": {"123:SECRET"}, "telegram_target_id": {"42"}}, c)
	if w := do(h, "GET", "/admin/settings/integrations", nil, c); strings.Contains(w.Body.String(), "123:SECRET") {
		t.Fatal("secret rendered")
	}
	// an empty secret keeps the stored value; other fields still save
	do(h, "POST", "/admin/settings/integrations", url.Values{"telegram_bot": {""}, "telegram_target_id": {"43"}}, c)
	if got := settingValues(t, q); got["telegram_bot"] != "123:SECRET" || got["telegram_target_id"] != "43" {
		t.Fatalf("secret not kept: %v", got)
	}
}

func TestSettingsValidation422(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	cases := []struct {
		tab  string
		form url.Values
		want string
	}{
		{"localisation", url.Values{"language": {"english"}, "timezone": {"Mars/Base"}, "country_code_phone": {"62"}}, "Unknown timezone"},
		{"integrations", url.Values{"sms_url": {"http://gw/send?to=x"}}, "URL must contain"},
		{"integrations", url.Values{"wa_url": {"http://gw/send?to=[number]"}}, "URL must contain"},
		{"integrations", url.Values{"webhook_url": {"ftp://x.test/hook"}}, "http or https"},
		{"integrations", url.Values{"smtp_port": {"70000"}}, "Enter a port"},
		{"notifications", url.Values{"reminder_hour": {"24"}}, "Enter a whole number"},
	}
	for _, tc := range cases {
		w := do(h, "POST", "/admin/settings/"+tc.tab, tc.form, c)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s %v: got %d", tc.tab, tc.form, w.Code)
		}
		if tc.tab == "localisation" && !strings.Contains(w.Body.String(), "Mars/Base") {
			t.Fatal("entered timezone not kept")
		}
	}
	if got := settingValues(t, q); len(got) != 0 {
		t.Fatalf("saved despite errors: %v", got)
	}
}

func TestSettingsAdminRole(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "bob")
	for _, tab := range []string{"app", "localisation", "notifications", "miscellaneous"} {
		if w := do(h, "GET", "/admin/settings/"+tab, nil, c); w.Code != 200 {
			t.Fatalf("admin %s: %d", tab, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/settings/integrations", nil, c); w.Code != http.StatusForbidden {
		t.Fatalf("admin integrations: %d", w.Code)
	}
	if w := do(h, "POST", "/admin/settings/integrations", url.Values{"telegram_target_id": {"1"}}, c); w.Code != http.StatusForbidden {
		t.Fatalf("admin integrations post: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/settings/app", nil, login(t, h, "rita")); w.Code != http.StatusForbidden {
		t.Fatalf("report role: %d", w.Code)
	}
}

func TestSettingsChangedCalledOnSave(t *testing.T) {
	s, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	calls := 0
	s.SettingsChanged = func(context.Context) { calls++ }
	do(h, "POST", "/admin/settings/app", url.Values{"company_name": {""}, "currency_code": {"Rp"}}, c)
	if calls != 0 {
		t.Fatal("hook called on a rejected save")
	}
	do(h, "POST", "/admin/settings/app", url.Values{"company_name": {"Acme"}, "currency_code": {"Rp"}}, c)
	if calls != 1 {
		t.Fatalf("hook calls: %d", calls)
	}
}
