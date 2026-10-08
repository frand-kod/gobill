package web

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func setting(t *testing.T, e *billEnv, kv ...string) {
	t.Helper()
	for i := 0; i < len(kv); i += 2 {
		if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: kv[i], Value: kv[i+1]}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPortalVoucherOnceAndDisabled(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	p := e.plan(t, "Gold", "PPPoE", 10000)
	if _, err := e.q.CreateVoucher(t.Context(), db.CreateVoucherParams{Code: "ABC", PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	c, _ := custLogin(t, e, "u1", "pw12345")
	form := url.Values{"code": {"ABC"}}
	setting(t, e, "disable_voucher", "yes")
	if w := do(e.h, "POST", "/portal/voucher", form, c); w.Code != http.StatusForbidden {
		t.Fatalf("disabled: %d", w.Code)
	}
	setting(t, e, "disable_voucher", "no", "voucher_redirect", "javascript:alert(1)")
	if w := do(e.h, "POST", "/portal/voucher", form, c); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal/orders" {
		t.Fatalf("redeem: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := do(e.h, "POST", "/portal/voucher", form, c); w.Code != 200 || !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatalf("second redeem: %d", w.Code)
	}
	if trx, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true}, Limit: 10}); len(trx) != 1 {
		t.Fatalf("trx %d", len(trx))
	}
	// a valid redirect URL is followed
	e.q.CreateVoucher(t.Context(), db.CreateVoucherParams{Code: "DEF", PlanID: p.ID})
	setting(t, e, "voucher_redirect", "https://example.com/ok")
	if w := do(e.h, "POST", "/portal/voucher", url.Values{"code": {"DEF"}}, c); w.Header().Get("Location") != "https://example.com/ok" {
		t.Fatalf("redirect: %s", w.Header().Get("Location"))
	}
}

func TestPortalTransfer(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 5000)
	e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Two", ServiceType: "PPPoE", Status: "Active"})
	c, _ := custLogin(t, e, "u1", "pw12345")
	f := url.Values{"username": {"u2"}, "balance": {"1000"}}
	if w := do(e.h, "POST", "/portal/transfer", f, c); w.Code != 200 || !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatalf("disabled: %d", w.Code)
	}
	setting(t, e, "allow_balance_transfer", "yes", "minimum_transfer", "500")
	if w := do(e.h, "POST", "/portal/transfer", url.Values{"username": {"u1"}, "balance": {"1000"}}, c); !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatal("self transfer accepted")
	}
	if w := do(e.h, "POST", "/portal/transfer", url.Values{"username": {"u2"}, "balance": {"100"}}, c); !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatal("below minimum accepted")
	}
	if w := do(e.h, "POST", "/portal/transfer", f, c); w.Code != http.StatusSeeOther {
		t.Fatalf("transfer: %d", w.Code)
	}
	u2, _ := e.q.GetCustomerByUsername(t.Context(), "u2")
	if cu, _ := e.q.GetCustomer(t.Context(), e.cust.ID); cu.Balance != 4000 || u2.Balance != 1000 {
		t.Fatalf("balances %d %d", cu.Balance, u2.Balance)
	}
}

func TestPortalRegistrationOptions(t *testing.T) {
	e := billApp(t)
	f := url.Values{"username": {"newbie"}, "fullname": {"New Bie"}, "password": {"abc123"}, "cpassword": {"abc123"}}
	setting(t, e, "disable_registration", "noreg")
	if w := do(e.h, "POST", "/portal/register", f, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal/login" {
		t.Fatalf("disabled: %d", w.Code)
	}
	if _, err := e.q.GetCustomerByUsername(t.Context(), "newbie"); err == nil {
		t.Fatal("registered while disabled")
	}
	setting(t, e, "disable_registration", "", "registration_username", "phone")
	if w := do(e.h, "POST", "/portal/register", f, nil); w.Code != 200 {
		t.Fatalf("non-phone username accepted: %d", w.Code)
	}
	f.Set("username", "081234567890")
	if w := do(e.h, "POST", "/portal/register", f, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("phone username: %d", w.Code)
	}
}

func TestPortalContactOTP(t *testing.T) {
	e, otp := forgotApp(t)
	c, _ := custLogin(t, e, "u1", "pw12345")
	// OTP disabled: endpoint absent, profile edit changes the phone directly
	if w := do(e.h, "POST", "/portal/contact/phone/otp", url.Values{"value": {"081111111111"}}, c); w.Code != http.StatusNotFound {
		t.Fatalf("disabled: %d", w.Code)
	}
	setting(t, e, "allow_phone_otp", "yes", "phone_otp_type", "sms")
	do(e.h, "POST", "/portal/profile", url.Values{"fullname": {"U One"}, "phone": {"089999999999"}}, c)
	if cu, _ := e.q.GetCustomer(t.Context(), e.cust.ID); cu.Phone != "081234567890" {
		t.Fatalf("phone edited without OTP: %s", cu.Phone)
	}
	if w := do(e.h, "POST", "/portal/contact/phone/otp", url.Values{"value": {"081111111111"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	code := otp()
	for i := 0; i < 4; i++ { // wrong codes burn tries but the flow survives until the 5th
		do(e.h, "POST", "/portal/contact/phone/verify", url.Values{"otp": {"000000"}}, c)
	}
	if w := do(e.h, "POST", "/portal/contact/phone/verify", url.Values{"otp": {code}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("verify: %d", w.Code)
	}
	if cu, _ := e.q.GetCustomer(t.Context(), e.cust.ID); cu.Phone != "081111111111" {
		t.Fatalf("phone %s", cu.Phone)
	}
	// 5 wrong tries drop the flow
	do(e.h, "POST", "/portal/contact/phone/otp", url.Values{"value": {"082222222222"}}, c)
	good := otp()
	for i := 0; i < 5; i++ {
		do(e.h, "POST", "/portal/contact/phone/verify", url.Values{"otp": {"000000"}}, c)
	}
	do(e.h, "POST", "/portal/contact/phone/verify", url.Values{"otp": {good}}, c)
	if cu, _ := e.q.GetCustomer(t.Context(), e.cust.ID); cu.Phone != "081111111111" {
		t.Fatalf("phone changed after lockout: %s", cu.Phone)
	}
}

func TestPortalShowBandwidth(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	e.plan(t, "Gold", "PPPoE", 10000)
	c, _ := custLogin(t, e, "u1", "pw12345")
	if w := do(e.h, "GET", "/portal/plans", nil, c); strings.Contains(w.Body.String(), "Mbps") || strings.Contains(w.Body.String(), ">b<") {
		t.Fatal("bandwidth shown while disabled")
	}
	setting(t, e, "show_bandwidth_plan", "yes")
	if w := do(e.h, "GET", "/portal/plans", nil, c); !strings.Contains(w.Body.String(), ": b</p>") {
		t.Fatalf("bandwidth missing: %s", w.Body.String())
	}
}

func TestPortalLoginBrandingAndRegisterLink(t *testing.T) {
	e := billApp(t)
	if body := do(e.h, "GET", "/portal/login", nil, nil).Body.String(); !strings.Contains(body, "/portal/register") {
		t.Fatal("register link missing")
	}
	setting(t, e, "disable_registration", "noreg", "login_page_favicon", "fav.png")
	body := do(e.h, "GET", "/portal/login", nil, nil).Body.String()
	if strings.Contains(body, "/portal/register") {
		t.Fatal("register link shown while registration is disabled")
	}
	if !strings.Contains(body, `<link rel="icon" href="/uploads/fav.png">`) {
		t.Fatal("favicon link missing")
	}
}
