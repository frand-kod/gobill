package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWATestButton(t *testing.T) {
	_, h, q := settingsSetup(t)
	var got string
	wa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.Write([]byte(`{"code":"SUCCESS"}`))
	}))
	defer wa.Close()
	c := login(t, h, "alice")
	do(h, "POST", "/admin/settings/integrations", url.Values{"alt_wga_server_url": {wa.URL}, "alt_wga_password": {"PW1"}}, c)
	if v := settingValues(t, q); v["alt_wga_password"] != "PW1" || v["wa_test_phone"] != "" {
		t.Fatalf("saved wrong: %v", v)
	}
	// empty password in the form keeps the stored one
	do(h, "POST", "/admin/settings/integrations", url.Values{"alt_wga_server_url": {wa.URL}, "alt_wga_password": {""}}, c)
	if v := settingValues(t, q); v["alt_wga_password"] != "PW1" {
		t.Fatal("password not kept")
	}
	w := do(h, "POST", "/admin/settings/integrations/wa-test", url.Values{"alt_wga_server_url": {wa.URL}, "wa_test_phone": {"08123456789"}}, c)
	if w.Code != 200 || !strings.Contains(got, "@s.whatsapp.net") || strings.Contains(w.Body.String(), "PW1") {
		t.Fatalf("%d %s", w.Code, got)
	}
	wa.Close()
	w = do(h, "POST", "/admin/settings/integrations/wa-test", url.Values{"alt_wga_server_url": {wa.URL}, "wa_test_phone": {"08123456789"}}, c)
	if !strings.Contains(w.Body.String(), "GOWA") {
		t.Fatal("no plain error")
	}
	if w := do(h, "POST", "/admin/settings/integrations/wa-test", url.Values{}, login(t, h, "bob")); w.Code != 403 {
		t.Fatalf("admin allowed: %d", w.Code)
	}
}
