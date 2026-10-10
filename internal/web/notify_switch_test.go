package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// wa_url alone is enough for the phone OTP to be offered.
func TestOTPEnabledWithWAURLOnly(t *testing.T) {
	st := map[string]string{"sms_otp_registration": "yes", "wa_url": "http://gw.test/?to=[number]&text=[text]"}
	if !otpEnabled(st) {
		t.Fatal("wa_url only: OTP not enabled")
	}
	if otpEnabled(map[string]string{"sms_otp_registration": "yes"}) {
		t.Fatal("no gateway: OTP enabled")
	}
}

// notify_otp=no: no code is sent for registration or forgot password, and the answer is the same
// for known and unknown usernames.
func TestOTPSwitchOff(t *testing.T) {
	e := billApp(t)
	gw, sent := countingGateway(t)
	setting(t, e, "sms_url", gw, "sms_otp_registration", "yes", "notify_otp", "no")
	msg := tr("Verification code is not available right now")
	reg := url.Values{"username": {"nbsw1"}, "fullname": {"N"}, "phone_number": {"0812000001"},
		"password": {"abc12345"}, "cpassword": {"abc12345"}, "send_otp": {"1"}}
	if w := do(e.h, "POST", "/portal/register", reg, nil); !strings.Contains(w.Body.String(), msg) {
		t.Fatalf("register: %d", w.Code)
	}
	portalCust(t, e, 0)
	for _, u := range []string{"u1", "nobody"} {
		if w := do(e.h, "POST", "/portal/forgot", url.Values{"username": {u}}, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), msg) {
			t.Fatalf("forgot %s: %d", u, w.Code)
		}
	}
	if sent.Load() != 0 {
		t.Fatalf("sent %d codes while off", sent.Load())
	}
}
