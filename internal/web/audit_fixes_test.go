package web

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A known and an unknown username get the same answer and session state.
func TestForgotSameAnswerKnownUnknown(t *testing.T) {
	e, _ := forgotApp(t)
	msg := tr("If your Username is found, Verification Code has been Sent to Your Phone/Email/Whatsapp")
	for _, u := range []string{"u1", "nobody"} {
		w := do(e.h, "POST", "/portal/forgot", url.Values{"username": {u}}, nil)
		if w.Code != 200 || sessionOf(w) == nil || !strings.Contains(w.Body.String(), msg) || !strings.Contains(w.Body.String(), `name="otp_code"`) {
			t.Fatalf("%s: got %d, want the same sent answer", u, w.Code)
		}
	}
}

func TestForgotResetMinimumLength(t *testing.T) {
	e, otp := forgotApp(t)
	ck := sessionOf(do(e.h, "POST", "/portal/forgot", url.Values{"username": {"u1"}}, nil))
	do(e.h, "POST", "/portal/forgot/verify", url.Values{"otp_code": {otp()}}, ck)
	w := do(e.h, "POST", "/portal/forgot/reset", url.Values{"npass": {"newpas7"}, "cnpass": {"newpas7"}}, ck)
	if !strings.Contains(w.Body.String(), tr("Password should be between 8 to 35 characters")) {
		t.Fatal("7-character password accepted")
	}
}

// Report role may not print unused vouchers (bearer credentials); the create role may.
func TestVoucherPrintRoles(t *testing.T) {
	s, h, _, c := crudApp(t)
	report := roleLogin(t, s, h, "Report")
	wantCode(t, do(h, "GET", "/admin/vouchers/print", nil, report), 403, "report prints vouchers")
	wantCode(t, do(h, "GET", "/admin/vouchers/print", nil, c), 200, "superadmin prints vouchers")
}

func TestCustomerPasswordMinimum(t *testing.T) {
	_, h, _, c := crudApp(t)
	form := url.Values{"username": {"pwcust"}, "password": {"1234567"}, "fullname": {"Pw Cust"}, "service_type": {"Hotspot"}, "status": {"Active"}}
	wantCode(t, do(h, "POST", "/admin/customers", form, c), 422, "7-character password")
	form.Set("password", "12345678")
	wantCode(t, do(h, "POST", "/admin/customers", form, c), 303, "8-character password")
}

// The failure maps drop keys with nothing recent once they grow past 10000.
func TestFailureMapsBounded(t *testing.T) {
	s, _ := newTestApp(t)
	stale := time.Now().Add(-2 * time.Hour)
	s.mu.Lock()
	s.otpSent = map[string][]time.Time{}
	for i := 0; i < 10001; i++ {
		s.failed[fmt.Sprint("stale", i)] = []time.Time{stale}
		s.otpSent[fmt.Sprint("ip:stale", i)] = []time.Time{stale}
	}
	s.mu.Unlock()
	s.recordFailure("1.2.3.4")
	s.otpAllow("5.6.7.8", "")
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failed) > 10 || len(s.otpSent) > 10 {
		t.Fatalf("maps not bounded: failed=%d otpSent=%d", len(s.failed), len(s.otpSent))
	}
}
