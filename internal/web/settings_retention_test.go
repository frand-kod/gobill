package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// radius_rest_allow is SuperAdmin only: an Admin neither sees the field nor saves it.
func TestSettingsRadiusAllowSuperAdminOnly(t *testing.T) {
	_, h, q := settingsSetup(t)
	bob, alice := login(t, h, "bob"), login(t, h, "alice")
	if body := do(h, "GET", "/admin/settings/system", nil, bob).Body.String(); strings.Contains(body, `name="radius_rest_allow"`) {
		t.Fatal("admin sees radius_rest_allow")
	}
	if w := do(h, "POST", "/admin/settings/system", url.Values{"radius_rest_allow": {"10.9.9.9"}}, bob); w.Code != http.StatusSeeOther {
		t.Fatalf("admin save: %d", w.Code)
	}
	if got := settingValues(t, q)["radius_rest_allow"]; got != "" {
		t.Fatalf("admin changed radius_rest_allow to %q", got)
	}
	if body := do(h, "GET", "/admin/settings/system", nil, alice).Body.String(); !strings.Contains(body, `name="radius_rest_allow"`) {
		t.Fatal("superadmin does not see radius_rest_allow")
	}
	if w := do(h, "POST", "/admin/settings/system", url.Values{"radius_rest_allow": {"10.0.0.0/24, 192.168.1.10"}}, alice); w.Code != http.StatusSeeOther {
		t.Fatalf("superadmin save: %d", w.Code)
	}
	if got := settingValues(t, q)["radius_rest_allow"]; got != "10.0.0.0/24, 192.168.1.10" {
		t.Fatalf("not persisted: %q", got)
	}
}

func TestSettingsRadiusAllowValidation(t *testing.T) {
	_, h, q := settingsSetup(t)
	alice := login(t, h, "alice")
	for _, bad := range []string{"10.0.0.0/33", "not-an-ip", "10.0.0.1, 999.1.1.1"} {
		w := do(h, "POST", "/admin/settings/system", url.Values{"radius_rest_allow": {bad}}, alice)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Enter IP addresses or CIDR ranges") {
			t.Fatalf("%q: got %d", bad, w.Code)
		}
	}
	if got := settingValues(t, q)["radius_rest_allow"]; got != "" {
		t.Fatalf("invalid list stored: %q", got)
	}
}

// The allow-list is read on every request, so a saved change applies at once.
func TestSettingsRadiusAllowUsed(t *testing.T) {
	_, h, _ := settingsSetup(t)
	alice := login(t, h, "alice")
	call := func(remote string) int {
		return post(h, "/radius.php?action=authorize", url.Values{"username": {"nobody"}, "password": {"x"}}, remote).Code
	}
	if c := call("10.0.0.5:1"); c != http.StatusForbidden {
		t.Fatalf("before save: %d, want 403", c)
	}
	if w := do(h, "POST", "/admin/settings/system", url.Values{"radius_rest_allow": {"10.0.0.0/24"}}, alice); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", w.Code)
	}
	if c := call("10.0.0.5:1"); c == http.StatusForbidden {
		t.Fatal("10.0.0.5 in 10.0.0.0/24 is still refused")
	}
	if c := call("10.0.1.5:1"); c != http.StatusForbidden {
		t.Fatalf("10.0.1.5 outside the list: %d", c)
	}
}

// log_keep_days and backup_keep: shown with their code defaults, validated, saved.
func TestSettingsRetentionFields(t *testing.T) {
	_, h, q := settingsSetup(t)
	alice := login(t, h, "alice")
	body := do(h, "GET", "/admin/settings/system", nil, alice).Body.String()
	for _, want := range []string{`name="log_keep_days"`, `value="90"`, `name="backup_keep"`, `value="7"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("default not shown: missing %s", want)
		}
	}
	for _, bad := range []url.Values{{"log_keep_days": {"-1"}}, {"log_keep_days": {"x"}}, {"backup_keep": {"0"}}} {
		if w := do(h, "POST", "/admin/settings/system", bad, alice); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%v: got %d", bad, w.Code)
		}
	}
	if w := do(h, "POST", "/admin/settings/system", url.Values{"log_keep_days": {"30"}, "backup_keep": {"5"}}, alice); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", w.Code)
	}
	got := settingValues(t, q)
	if got["log_keep_days"] != "30" || got["backup_keep"] != "5" {
		t.Fatalf("not persisted: %v", got)
	}
}
