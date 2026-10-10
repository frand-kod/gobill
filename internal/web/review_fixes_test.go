package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestPortalVoucherThrottled(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	p := e.plan(t, "Gold", "PPPoE", 10000)
	e.q.CreateVoucher(t.Context(), db.CreateVoucherParams{Code: "GOODCODE1", PlanID: p.ID})
	c, _ := custLogin(t, e, "u1", "pw12345")
	for i := 0; i < maxFailedLogins; i++ {
		do(e.h, "POST", "/portal/voucher", url.Values{"code": {"BAD" + strconv.Itoa(i)}}, c)
	}
	if w := do(e.h, "POST", "/portal/voucher", url.Values{"code": {"GOODCODE1"}}, c); w.Code != http.StatusTooManyRequests {
		t.Fatalf("valid code after failures: %d", w.Code)
	}
}

func TestVoucherGenerateRejectsShort(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	for fmtName, l := range map[string]string{"up": "7", "numbers": "9"} {
		w := do(e.h, "POST", "/admin/vouchers", url.Values{"plan": {itoa(p.ID)}, "numbervoucher": {"1"}, "voucher_format": {fmtName}, "lengthcode": {l}}, e.c)
		if w.Code == http.StatusSeeOther {
			t.Fatalf("%s length %s accepted", fmtName, l)
		}
	}
}

func countingGateway(t *testing.T) (string, *atomic.Int32) {
	var n atomic.Int32
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1) }))
	t.Cleanup(gw.Close)
	return gw.URL + "/?to=[number]&text=[text]", &n
}

func TestForgotOTPPhoneLimit(t *testing.T) {
	e := billApp(t)
	gw, sent := countingGateway(t)
	c := e.cust
	e.q.UpdateCustomer(t.Context(), db.UpdateCustomerParams{Fullname: c.Fullname, Phone: "081234567890", ServiceType: c.ServiceType, AutoRenewal: 1, Status: "Active", ID: c.ID})
	setting(t, e, "sms_url", gw)
	portalCust(t, e, 0)
	f := url.Values{"username": {"u1"}}
	do(e.h, "POST", "/portal/forgot", f, nil)
	w := do(e.h, "POST", "/portal/forgot", f, nil) // fresh session: only the server-side limit can stop it
	if sent.Load() != 1 || w.Code != http.StatusTooManyRequests {
		t.Fatalf("sent %d code %d", sent.Load(), w.Code)
	}
}

func TestOTPAllowLimits(t *testing.T) {
	s := &Server{}
	if !s.otpAllow("1.1.1.1", "081") || s.otpAllow("1.1.1.1", "081") {
		t.Fatal("per-phone cooldown")
	}
	for i := 0; i < 4; i++ { // ip has used 1; distinct phones
		if !s.otpAllow("1.1.1.1", "09"+strconv.Itoa(i)) {
			t.Fatalf("send %d refused early", i)
		}
	}
	if s.otpAllow("1.1.1.1", "0999") {
		t.Fatal("per-IP limit not enforced")
	}
	if !s.otpAllow("2.2.2.2", "0999") {
		t.Fatal("other IP should pass")
	}
}

func TestRegisterOTPSendLimited(t *testing.T) {
	e := billApp(t)
	gw, sent := countingGateway(t)
	setting(t, e, "sms_url", gw, "sms_otp_registration", "yes")
	for i := 0; i < 6; i++ {
		do(e.h, "POST", "/portal/register", url.Values{"username": {"nb" + strconv.Itoa(i)}, "fullname": {"N"}, "phone_number": {"0812000000" + strconv.Itoa(i)},
			"password": {"abc12345"}, "cpassword": {"abc12345"}, "send_otp": {"1"}}, nil)
	}
	if sent.Load() != 5 {
		t.Fatalf("sent %d, want 5", sent.Load())
	}
}

func TestCSVSafeTriggers(t *testing.T) {
	rec := []string{"=1", "+1", "-1", "@a", "\tx", "\rx", "ok", ""}
	csvSafe(rec)
	for i, w := range []string{"'=1", "'+1", "'-1", "'@a", "'\tx", "'\rx", "ok", ""} {
		if rec[i] != w {
			t.Fatalf("%d: %q", i, rec[i])
		}
	}
}
