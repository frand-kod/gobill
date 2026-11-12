package web

// Security audit 2026-11-01: each test asserts the SAFE behaviour and fails on the current code.

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/core"
)

// #1: the Report role must not read unused voucher codes (each one is free service).
func TestAuditSec1_ReportSeesVoucherCodes(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "Hot", "Hotspot", 5000)
	if _, err := e.q.CreateVoucher(t.Context(), db.CreateVoucherParams{Code: "SECRETCODE42", PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/admin/vouchers", nil, login(t, e.h, "rita"))
	if w.Code == 200 && strings.Contains(w.Body.String(), "SECRETCODE42") {
		t.Fatal("Report role sees an unused voucher code in plaintext")
	}
}

// #3: changing a router's host must not keep the stored password (it would be sent to the new host on Test).
func TestAuditSec3_RouterHostChangeKeepsPassword(t *testing.T) {
	e := billApp(t)
	f := url.Values{"name": {"r"}, "host": {"203.0.113.9"}, "port": {"8728"}, "username": {"u"}, "enabled": {"1"}, "coverage": {"0"}}
	w := do(e.h, "POST", fmt.Sprint("/admin/routers/", e.rt), f, e.c)
	if w.Code == 303 {
		rt, _ := e.q.GetRouter(t.Context(), e.rt)
		t.Fatalf("host changed to %s with the stored password kept and no re-entry", rt.Host)
	}
}

// #4: trusted_proxies must reject a catch-all range, and only a SuperAdmin may set it.
func TestAuditSec4_TrustedProxiesCatchAll(t *testing.T) {
	if _, ok := parseTrustedProxies("0.0.0.0/0"); ok {
		t.Error("0.0.0.0/0 accepted: any client may spoof X-Forwarded-For and dodge per-IP throttles")
	}
	if !settingsSuperField["trusted_proxies"] || !settingsSuperField["trust_proxy"] {
		t.Error("trust_proxy/trusted_proxies are editable by the Admin role")
	}
}

// #5: one pending 2FA login must not allow unbounded guesses when they come from many IPs.
func TestAuditSec5_2FAGuessesAcrossIPs(t *testing.T) {
	old := core.LoginDelayStep
	core.LoginDelayStep = 0
	t.Cleanup(func() { core.LoginDelayStep = old })
	s, _ := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	h := s.Handler()
	key, _ := enrollTOTP(t, s, h, login(t, h, "alice"), "alice")
	pending := sessionCookie(passwordStep(h, "alice"))
	post := func(code, ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/admin/login/2fa", strings.NewReader("code="+code))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = ip + ":1234"
		r.AddCookie(pending)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	n := 5 * core.MaxFailedLogins
	for i := 0; i < n; i++ {
		post("000000", fmt.Sprintf("198.51.100.%d", i%250+1))
	}
	w := post(totpCode(key, time.Now().Unix()/30+1), "198.51.100.251")
	if w.Code == 303 && w.Header().Get("Location") == "/admin" {
		t.Fatalf("pending 2FA session still accepted a code after %d wrong guesses from rotating IPs", n)
	}
}

// #7: open registration must be rate limited per client.
func TestAuditSec7_RegistrationUnthrottled(t *testing.T) {
	s, q := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	h := s.Handler()
	for i := 0; i < 30; i++ {
		f := url.Values{"username": {fmt.Sprintf("bot%04d", i)}, "phone_number": {fmt.Sprintf("08120000%04d", i)}, "password": {"Xk9!long-pass-77"}}
		do(h, "POST", "/portal/register", f, nil)
	}
	if _, err := q.GetCustomerByUsername(t.Context(), "bot0029"); err == nil {
		t.Fatal("30 registrations from one IP all succeeded")
	}
}

// #8: restore and import replace the database; their confirm asks for the current password like the key download.
func TestAuditSec8_RestoreNoStepUp(t *testing.T) {
	s, h, c := restoreApp(t)
	w := restoreUpload(t, h, c, backupFile(t, s, nil))
	if !strings.Contains(w.Body.String(), `name="current"`) {
		t.Fatal("restore confirm has no current-password field")
	}
	token := importToken.FindStringSubmatch(w.Body.String())[1]
	for _, pw := range []string{"", "wrong"} {
		wantCode(t, do(h, "POST", restorePageURL+"/confirm", url.Values{"token": {token}, "understand": {"1"}, "current": {pw}}, c), 422, "password "+pw)
	}
	if _, err := os.Stat(s.DBPath + ".restore"); !os.IsNotExist(err) {
		t.Fatal("restore staged without the right password")
	}
	// import: same step-up
	tok := preview(t, h, c)
	wantCode(t, do(h, "POST", importPage+"/confirm", url.Values{"token": {tok}, "understand": {"1"}, "current": {"wrong"}}, c), 422, "import wrong password")
	wantCode(t, do(h, "POST", importPage+"/confirm", url.Values{"token": {tok}, "understand": {"1"}, "current": {"secret123"}}, c), 303, "import right password")
}
