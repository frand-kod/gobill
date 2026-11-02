package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/payment"
)

// D-L1: the Duitku button checks Duitku's keys even while Tripay is the active gateway.
func TestDuitkuTestUsesDuitkuKeys(t *testing.T) {
	s, h, q := settingsSetup(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":[{"code":"QRIS","active":true}]}`))
	}))
	defer fake.Close()
	var got map[string]string
	s.NewGateway = func(cfg map[string]string) (Gateway, error) {
		got = cfg
		g, err := payment.NewTripay(map[string]string{"api_key": cfg["api_key"], "private_key": "p", "merchant_code": "m"})
		if err == nil {
			g.BaseURL = fake.URL
		}
		return g, err
	}
	setSettings(t, q, map[string]string{"payment_gateway": "tripay", "duitku_api_key": "dk", "duitku_merchant_code": "D1", "duitku_mode": "production",
		"tripay_api_key": "tk", "tripay_private_key": "tp", "tripay_merchant_code": "T1"})
	c := login(t, h, "alice")
	p := integrationPage(t, h, c, "/admin/settings/payment/duitku-test", nil, "/admin/settings/payment")
	if got["api_key"] != "dk" || !strings.Contains(p, "Duitku connection OK (production mode") {
		t.Fatalf("cfg %v page %s", got, p)
	}
	integrationPage(t, h, c, "/admin/settings/payment/tripay-test", nil, "/admin/settings/payment")
	if got["api_key"] != "tk" {
		t.Fatalf("tripay button used %v", got)
	}
}

// D-M7: while the password is the imported sha1 one, the form explains why and offers Logout, not a Cancel loop.
// D-M6: the current password field asks for current-password, the new ones for new-password, with the length rule.
func TestForcedPasswordPage(t *testing.T) {
	s, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	p := do(h, "GET", "/admin/password", nil, c).Body.String()
	if strings.Contains(p, "old system") || !strings.Contains(p, `href="/admin">Cancel`) {
		t.Fatalf("normal page: %s", p)
	}
	if !strings.Contains(p, `id="current" name="current" type="password" value="" required autocomplete="current-password"`) ||
		!strings.Contains(p, `name="password" type="password" value="" required autocomplete="new-password"`) ||
		!strings.Contains(p, "10 to 72 characters") {
		t.Fatalf("autocomplete or hint missing: %s", p)
	}
	if _, err := s.conn.Exec("UPDATE admins SET legacy_sha1 = 'abc' WHERE username = 'alice'"); err != nil {
		t.Fatal(err)
	}
	p = do(h, "GET", "/admin/password", nil, c).Body.String()
	if !strings.Contains(p, "old system") || strings.Contains(p, `href="/admin">Cancel`) || !strings.Contains(p, `formaction="/logout"`) {
		t.Fatalf("forced page: %s", p)
	}
}
