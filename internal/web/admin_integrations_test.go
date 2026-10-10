package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

func setSettings(t *testing.T, q *db.Queries, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
}

// integrationPage posts a test and returns the Integrations page that shows its flash or error.
func integrationPage(t *testing.T, h http.Handler, c *http.Cookie, path string, form url.Values, page string) string {
	t.Helper()
	if w := do(h, "POST", path, form, c); w.Code != http.StatusSeeOther {
		t.Fatalf("%s: %d", path, w.Code)
	}
	return do(h, "GET", page, nil, c).Body.String()
}

func TestIntegrationTestSuperAdminOnly(t *testing.T) {
	_, h, _ := settingsSetup(t)
	bob := login(t, h, "bob")
	for _, p := range []string{"telegram", "sms", "email", "webhook"} {
		if w := do(h, "POST", "/admin/settings/integrations/test/"+p, url.Values{}, bob); w.Code != http.StatusForbidden {
			t.Fatalf("%s: admin got %d", p, w.Code)
		}
	}
	if w := do(h, "POST", "/admin/settings/payment/tripay-test", url.Values{}, bob); w.Code != http.StatusForbidden {
		t.Fatalf("tripay: admin got %d", w.Code)
	}
}

func TestIntegrationTestSMSAndWebhook(t *testing.T) {
	_, h, q := settingsSetup(t)
	var smsTo, hookBody, hookSig string
	var hookCode = http.StatusAccepted
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sms" {
			smsTo = r.URL.Query().Get("to")
			return
		}
		b, _ := io.ReadAll(r.Body)
		hookBody, hookSig = string(b), r.Header.Get("X-Signature")
		w.WriteHeader(hookCode)
	}))
	defer stub.Close()
	// notify_customers=no must not stop the test: it targets the operator
	setSettings(t, q, map[string]string{"notify_customers": "no", "sms_url": stub.URL + "/sms?to=[number]&text=[text]",
		"webhook_url": stub.URL + "/hook", "webhook_secret": "WHSEC"})
	c := login(t, h, "alice")

	if p := integrationPage(t, h, c, "/admin/settings/integrations/test/sms", url.Values{"sms_test_phone": {"08123456789"}}, "/admin/settings/integrations"); !strings.Contains(p, "SMS test sent") || smsTo != "08123456789" {
		t.Fatalf("sms: to=%q page=%s", smsTo, p)
	}
	if p := integrationPage(t, h, c, "/admin/settings/integrations/test/webhook", nil, "/admin/settings/integrations"); !strings.Contains(p, "Webhook answered HTTP 202") ||
		!strings.Contains(hookBody, `"event":"test"`) || !strings.HasPrefix(hookSig, "sha256=") {
		t.Fatalf("webhook: body=%s sig=%s page=%s", hookBody, hookSig, p)
	}
	hookCode = http.StatusInternalServerError
	p := integrationPage(t, h, c, "/admin/settings/integrations/test/webhook", nil, "/admin/settings/integrations")
	if !strings.Contains(p, "Test failed") || !strings.Contains(p, "HTTP 500") || strings.Contains(p, "WHSEC") {
		t.Fatalf("webhook failure: %s", p)
	}
	if p := integrationPage(t, h, c, "/admin/settings/integrations/test/sms", url.Values{}, "/admin/settings/integrations"); !strings.Contains(p, "Enter a phone number for the test") {
		t.Fatal("empty phone accepted")
	}
}

func TestIntegrationTestTelegram(t *testing.T) {
	s, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	const path = "/admin/settings/integrations/test/telegram"
	if p := integrationPage(t, h, c, path, nil, "/admin/settings/integrations"); !strings.Contains(p, "Fill in the settings of this channel") {
		t.Fatal("unset telegram not refused")
	}
	var got string
	var code = http.StatusOK
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.WriteHeader(code)
	}))
	defer stub.Close()
	s.telegramAPI = stub.URL
	setSettings(t, q, map[string]string{"telegram_bot": "123456:ABCSECRET", "telegram_target_id": "42"})
	if p := integrationPage(t, h, c, path, nil, "/admin/settings/integrations"); !strings.Contains(p, "Telegram test message sent") || got != "/bot123456:ABCSECRET/sendMessage" {
		t.Fatalf("telegram: path=%s page=%s", got, p)
	}
	code = http.StatusUnauthorized
	p := integrationPage(t, h, c, path, nil, "/admin/settings/integrations")
	if !strings.Contains(p, "Test failed") || !strings.Contains(p, "HTTP 401") || strings.Contains(p, "ABCSECRET") {
		t.Fatalf("telegram failure leaks or misses: %s", p)
	}
}

func TestIntegrationTestEmailFailureHidesPassword(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	// nothing listens on port 1: the send fails, and the SMTP password must not show
	setSettings(t, q, map[string]string{"smtp_host": "127.0.0.1", "smtp_port": "1", "mail_from": "ops@example.com", "smtp_pass": "PASSSECRET"})
	p := integrationPage(t, h, c, "/admin/settings/integrations/test/email", url.Values{"email_test_to": {"ops@example.com"}}, "/admin/settings/integrations")
	if !strings.Contains(p, "Test failed") || strings.Contains(p, "PASSSECRET") {
		t.Fatalf("email failure: %s", p)
	}
}

func TestIntegrationTestRateLimit(t *testing.T) {
	_, h, q := settingsSetup(t)
	hits := 0
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer stub.Close()
	setSettings(t, q, map[string]string{"webhook_url": stub.URL})
	c := login(t, h, "alice")
	for i := 0; i < 5; i++ {
		do(h, "POST", "/admin/settings/integrations/test/webhook", nil, c)
	}
	p := integrationPage(t, h, c, "/admin/settings/integrations/test/webhook", nil, "/admin/settings/integrations")
	if hits != 5 || !strings.Contains(p, "Too many tests") {
		t.Fatalf("hits=%d page=%s", hits, p)
	}
}

func TestIntegrationTestTripay(t *testing.T) {
	s, h, q := settingsSetup(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k1" {
			w.Write([]byte(`{"success":false,"message":"Unauthorized"}`))
			return
		}
		w.Write([]byte(`{"success":true,"data":[{"code":"QRIS","active":true},{"code":"BRIVA","active":false}]}`))
	}))
	defer fake.Close()
	s.NewGateway = func(cfg map[string]string) (Gateway, error) {
		g, err := payment.NewTripay(cfg)
		if err == nil {
			g.BaseURL = fake.URL
		}
		return g, err
	}
	setSettings(t, q, map[string]string{"payment_gateway": "tripay", "tripay_api_key": "k1", "tripay_private_key": "PRIVSECRET",
		"tripay_merchant_code": "M1", "tripay_mode": "sandbox"})
	c := login(t, h, "alice")
	const path, page = "/admin/settings/payment/tripay-test", "/admin/settings/payment"
	if p := integrationPage(t, h, c, path, nil, page); !strings.Contains(p, "Tripay connection OK (sandbox mode, 2 payment channels)") {
		t.Fatalf("ok: %s", p)
	}
	setSettings(t, q, map[string]string{"tripay_api_key": "bad"})
	p := integrationPage(t, h, c, path, nil, page)
	if !strings.Contains(p, "Test failed") || !strings.Contains(p, "Unauthorized") || strings.Contains(p, "PRIVSECRET") {
		t.Fatalf("failure: %s", p)
	}
}
