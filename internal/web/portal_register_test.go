package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// regForm posts the three-field self-registration form.
func regForm(e *billEnv, user, pass, phone string) *httptest.ResponseRecorder {
	return do(e.h, "POST", "/portal/register", url.Values{"username": {user}, "password": {pass}, "phone_number": {phone}}, nil)
}

// noCustomer fails when a customer with this exact login name exists.
func noCustomer(t *testing.T, e *billEnv, user string) {
	t.Helper()
	if _, err := e.q.GetCustomerByUsername(t.Context(), user); err != sql.ErrNoRows {
		t.Fatalf("customer %q created (lookup: %v)", user, err)
	}
}

func TestRegisterThreeFieldsLogsIn(t *testing.T) {
	e := billApp(t)
	w := regForm(e, "newbie", "abc12345", "0812-3456-7890")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal" {
		t.Fatalf("register: %d %s", w.Code, w.Header().Get("Location"))
	}
	c, err := e.q.GetCustomerByUsername(t.Context(), "newbie")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "Active" || c.Fullname != "newbie" || c.Phone != "6281234567890" || c.Email != "" || c.Address != "" {
		t.Fatalf("customer: status %q fullname %q phone %q email %q address %q", c.Status, c.Fullname, c.Phone, c.Email, c.Address)
	}
	ck := sessionOf(w)
	if ck == nil {
		t.Fatal("no session after register")
	}
	if d := do(e.h, "GET", "/portal", nil, ck); d.Code != http.StatusOK {
		t.Fatalf("dashboard after register: %d", d.Code)
	}
}

func TestRegisterUsernameNormalisedAndChecked(t *testing.T) {
	e := billApp(t)
	if w := regForm(e, "NewBie1", "abc12345", "0812000001"); w.Code != http.StatusSeeOther {
		t.Fatalf("uppercase username: %d", w.Code)
	}
	if _, err := e.q.GetCustomerByUsername(t.Context(), "newbie1"); err != nil {
		t.Fatalf("username not stored lower case: %v", err)
	}
	for _, u := range []string{"abc", "ab_cde", "abc-de", "budi.s", "abc de", strings.Repeat("a", 33)} {
		if w := regForm(e, u, "abc12345", "0812000002"); w.Code != http.StatusOK {
			t.Fatalf("username %q: %d, want form error", u, w.Code)
		}
		noCustomer(t, e, u)
	}
}

func TestRegisterUsernameUnique(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	// pppoe_username of an existing customer is a login name too
	c := e.cust
	if err := e.q.UpdateCustomer(ctx, db.UpdateCustomerParams{Fullname: c.Fullname, ServiceType: c.ServiceType, AutoRenewal: 1,
		Status: "Active", PppoeUsername: "dialer01", ID: c.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "budi2024", PasswordHash: "x", Fullname: "Budi", ServiceType: "Others", Status: "Active"}); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t, "Gold", "PPPoE", 10000)
	if _, err := e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: "VCH7X", PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"Dialer01", "BUDI2024", "vch7x"} {
		if w := regForm(e, u, "abc12345", "0812000003"); w.Code != http.StatusOK {
			t.Fatalf("taken %q accepted: %d", u, w.Code)
		}
	}
	noCustomer(t, e, "dialer01")
	noCustomer(t, e, "vch7x")
}

func TestRegisterPasswordRules(t *testing.T) {
	e := billApp(t)
	for _, pass := range []string{"newbie", "NEWBIE", "1234", "Qwerty", "PASSWORD"} {
		if w := regForm(e, "newbie", pass, "0812000004"); w.Code != http.StatusOK {
			t.Fatalf("password %q accepted: %d", pass, w.Code)
		}
	}
	noCustomer(t, e, "newbie")
	if !strings.Contains(regForm(e, "newbie", "1234", "0812000004").Body.String(), "Password terlalu mudah ditebak") {
		t.Fatal("common password message missing")
	}
	// the setting raises the minimum; 5 characters fail at 6
	setting(t, e, "customer_password_min", "6")
	if w := regForm(e, "newbie", "abc12", "0812000004"); w.Code != http.StatusOK {
		t.Fatalf("5 chars at minimum 6: %d", w.Code)
	}
	noCustomer(t, e, "newbie")
	if w := regForm(e, "newbie", "abc123", "0812000004"); w.Code != http.StatusSeeOther {
		t.Fatalf("6 chars at minimum 6: %d", w.Code)
	}
}

func TestRegisterWANumberRequiredAndUnique(t *testing.T) {
	e := billApp(t)
	for _, phone := range []string{"", "123"} {
		if w := regForm(e, "newbie", "abc12345", phone); w.Code != http.StatusOK {
			t.Fatalf("phone %q accepted: %d", phone, w.Code)
		}
	}
	noCustomer(t, e, "newbie")
	c := e.cust
	if err := e.q.UpdateCustomer(t.Context(), db.UpdateCustomerParams{Fullname: c.Fullname, Phone: "081234567890",
		ServiceType: c.ServiceType, AutoRenewal: 1, Status: "Active", ID: c.ID}); err != nil {
		t.Fatal(err)
	}
	setting(t, e, "wa_unique_per_account", "yes") // unique per account is opt-in; the default lets numbers be shared
	w := regForm(e, "newbie", "abc12345", "0812-3456-7890")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "sudah dipakai akun lain") { // billApp is Indonesian
		t.Fatalf("number of another customer accepted: %d", w.Code)
	}
	noCustomer(t, e, "newbie")
}

func TestRegisterWithWAOTP(t *testing.T) {
	e := billApp(t)
	gw, last := fakeGateway(t)
	setting(t, e, "sms_url", gw.URL+"/?to=[number]&text=[text]", "sms_otp_registration", "yes")
	form := url.Values{"username": {"newbie"}, "password": {"abc12345"}, "phone_number": {"0812000005"}, "send_otp": {"1"}}
	w := do(e.h, "POST", "/portal/register", form, nil)
	if !strings.Contains(w.Body.String(), tr("Verification code sent")) {
		t.Fatalf("code not sent: %d", w.Code)
	}
	ck := sessionOf(w)
	var code string
	for i := 0; i < 100 && code == ""; i++ {
		code = otpRe.FindString(last())
		time.Sleep(20 * time.Millisecond)
	}
	if code == "" {
		t.Fatal("no OTP sent")
	}
	noCustomer(t, e, "newbie")
	form.Del("send_otp")
	form.Set("otp_code", "000000")
	if w := do(e.h, "POST", "/portal/register", form, ck); !strings.Contains(w.Body.String(), tr("Wrong Verification code")) {
		t.Fatalf("wrong code: %d", w.Code)
	}
	noCustomer(t, e, "newbie")
	form.Set("otp_code", code)
	if w := do(e.h, "POST", "/portal/register", form, ck); w.Code != http.StatusSeeOther {
		t.Fatalf("right code: %d", w.Code)
	}
	if _, err := e.q.GetCustomerByUsername(t.Context(), "newbie"); err != nil {
		t.Fatalf("customer not created after the right code: %v", err)
	}
}

func TestSettingCustomerPasswordMinValidation(t *testing.T) {
	e := billApp(t)
	for v, bad := range map[string]bool{"3": true, "4": false, "64": false, "65": true} {
		if got := e.s.settingsErrors(map[string]string{"customer_password_min": v})["customer_password_min"] != ""; got != bad {
			t.Fatalf("customer_password_min %s: error=%v", v, got)
		}
	}
}
