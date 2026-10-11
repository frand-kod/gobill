package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/i18n"
)

// The tab order and slugs are what the sidebar and the search index rely on.
func TestSettingsTabOrder(t *testing.T) {
	var got []string
	for _, tab := range settingsTabs {
		got = append(got, tab.Slug+"="+tab.Label)
	}
	want := "app=General localisation=Localisation billing=Customers & Billing payment=Payments integrations=Integrations " +
		"notifications=Notifications isolir=Isolation promo=Promotions appearance=Appearance system=System & Security"
	if strings.Join(got, " ") != want {
		t.Fatalf("tabs:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

// Each setting key is on exactly one tab, so a save of one tab never writes a key of another.
func TestSettingsKeysOnOneTab(t *testing.T) {
	s, _, _ := settingsSetup(t)
	owner := map[string]string{}
	for _, tab := range settingsTabs {
		for _, n := range fieldNames(s.settingsFields(tab.Slug, nil, nil, nil)) {
			if prev, ok := owner[n]; ok {
				t.Errorf("%s is on tabs %s and %s", n, prev, tab.Slug)
			}
			owner[n] = tab.Slug
		}
	}
	for k, tab := range map[string]string{"login_page_head": "appearance", "login_page_logo": "appearance",
		"login_page_description": "appearance", "login_page_wallpaper": "appearance", "company_name": "app",
		"session_timeout_duration": "system", "voucher_format": "billing", "tripay_mode": "payment"} {
		if owner[k] != tab {
			t.Errorf("%s on %q, want %q", k, owner[k], tab)
		}
	}
}

// Section titles are English keys that have an Indonesian text in lang/indonesia.json.
func TestSettingsTitlesTranslated(t *testing.T) {
	s, _, _ := settingsSetup(t)
	cat, err := i18n.Load(gobill.FS, "lang")
	if err != nil {
		t.Fatal(err)
	}
	id := cat["indonesia"]
	nonWord := regexp.MustCompile(`[^A-Za-z0-9]`)
	check := func(where, title string) {
		if title == "" {
			return
		}
		if id[title] == "" && id[nonWord.ReplaceAllString(title, "_")] == "" {
			t.Errorf("%s: section %q has no key in lang/indonesia.json", where, title)
		}
	}
	for _, tab := range settingsTabs {
		check(tab.Slug, tab.Label)
		for _, f := range s.settingsFields(tab.Slug, nil, nil, nil) {
			check(tab.Slug, f.Section)
		}
	}
	for _, f := range s.isolirSetupFields(nil) {
		check("isolir setup", f.Section)
	}
	check("integrations", "New token") // the one-time metrics token card
}

// The miscellaneous tab is gone; its page is a 404 like any unknown tab.
func TestSettingsMiscellaneousGone(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	if w := do(h, "GET", "/admin/settings/miscellaneous", nil, c); w.Code != http.StatusNotFound {
		t.Fatalf("GET miscellaneous: %d", w.Code)
	}
	if w := do(h, "POST", "/admin/settings/miscellaneous", nil, c); w.Code != http.StatusNotFound {
		t.Fatalf("POST miscellaneous: %d", w.Code)
	}
}
