package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	nuxbill "github.com/frand-kod/nuxbill-go"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/i18n"
)

// tr is the text the default language (indonesia) shows for msg.
func tr(msg string) string {
	c, err := i18n.Load(nuxbill.FS, "lang")
	if err != nil {
		panic(err)
	}
	return c.T("indonesia", msg)
}

func TestCustomFieldRequiredAndSave(t *testing.T) {
	e := billApp(t)
	c := e.c
	if w := do(e.h, "POST", "/admin/fields", url.Values{"name": {"Shoe Size"}, "type": {"number"}, "required": {"1"}, "sort_order": {"1"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("create number field: %d", w.Code)
	}
	if w := do(e.h, "POST", "/admin/fields", url.Values{"name": {"Tier"}, "type": {"select"}, "options": {"Gold, Silver"}, "sort_order": {"2"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("create select field: %d", w.Code)
	}
	if w := do(e.h, "POST", "/admin/fields", url.Values{"name": {"Tier"}, "type": {"text"}}, c); w.Code != 422 {
		t.Fatalf("duplicate name: %d", w.Code)
	}
	defs, err := e.q.ListCustomFields(t.Context())
	if err != nil || len(defs) != 2 {
		t.Fatalf("fields: %v %d", err, len(defs))
	}
	shoe, tier := defs[0], defs[1]

	base := url.Values{"fullname": {"U One"}, "service_type": {"PPPoE"}, "status": {"Active"}, "auto_renewal": {"1"}}
	save := func(shoeVal, tierVal string) *httptest.ResponseRecorder {
		f := url.Values{}
		for k, v := range base {
			f[k] = v
		}
		f.Set(cfKey(shoe.ID), shoeVal)
		f.Set(cfKey(tier.ID), tierVal)
		return do(e.h, "POST", fmt.Sprint("/admin/customers/", e.cust.ID), f, c)
	}

	// required number missing: form again with the error
	if w := save("", "Gold"); w.Code != 422 || !strings.Contains(w.Body.String(), tr("This field is required")) {
		t.Fatalf("required: %d", w.Code)
	}
	if w := save("abc", "Gold"); w.Code != 422 || !strings.Contains(w.Body.String(), tr("Enter a number")) {
		t.Fatalf("number: %d", w.Code)
	}
	if w := save("42", "Platinum"); w.Code != 422 || !strings.Contains(w.Body.String(), tr("Invalid value")) {
		t.Fatalf("select: %d", w.Code)
	}
	if w := save("42", "Silver"); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", w.Code)
	}
	vals, err := e.q.ListCustomerFieldValues(t.Context(), e.cust.ID)
	if err != nil || len(vals) != 2 {
		t.Fatalf("stored values: %v %d", err, len(vals))
	}
	w := do(e.h, "GET", fmt.Sprint("/admin/customers/", e.cust.ID), nil, c)
	if !strings.Contains(w.Body.String(), "Shoe Size") || !strings.Contains(w.Body.String(), "Silver") {
		t.Fatal("detail page misses custom values")
	}
	// the edit form is prefilled with the stored value
	w = do(e.h, "GET", fmt.Sprint("/admin/customers/", e.cust.ID, "/edit"), nil, c)
	if !strings.Contains(w.Body.String(), `value="42"`) {
		t.Fatal("edit form not prefilled")
	}
}

func TestPageRendersEscaped(t *testing.T) {
	e := billApp(t)
	if err := e.q.UpdatePageBody(t.Context(), db.UpdatePageBodyParams{Body: "<script>alert(1)</script>\nline two", Slug: "announcement"}); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/pages/announcement", nil, nil)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, "<script>alert(1)") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("page not escaped: %d", w.Code)
	}
	if !strings.Contains(body, "whitespace-pre-line") {
		t.Fatal("line breaks not kept")
	}
	if w := do(e.h, "GET", "/pages/nope", nil, nil); w.Code != 404 {
		t.Fatalf("unknown page: %d", w.Code)
	}
}

// fakeGateway records the last SMS text it was asked to send.
func fakeGateway(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	last := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = r.URL.Query().Get("text")
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, func() string { mu.Lock(); defer mu.Unlock(); return last }
}

var otpRe = regexp.MustCompile(`\b\d{6}\b`)

// forgotApp is billApp with the customer's phone set and an SMS gateway pointing at the fake.
func forgotApp(t *testing.T) (*billEnv, func() string) {
	t.Helper()
	e := billApp(t)
	gw, last := fakeGateway(t)
	c := e.cust
	if err := e.q.UpdateCustomer(t.Context(), db.UpdateCustomerParams{Fullname: c.Fullname, Phone: "081234567890",
		ServiceType: c.ServiceType, AutoRenewal: 1, Status: "Active", ID: c.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "sms_url", Value: gw.URL + "/?to=[number]&text=[text]"}); err != nil {
		t.Fatal(err)
	}
	portalCust(t, e, 0)
	return e, func() string {
		m := otpRe.FindString(last())
		if m == "" {
			t.Fatal("no OTP sent")
		}
		return m
	}
}

func sessionOf(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "nuxbill_session" {
			return c
		}
	}
	return nil
}

func TestForgotPasswordEndToEnd(t *testing.T) {
	e, otp := forgotApp(t)
	w := do(e.h, "POST", "/portal/forgot", url.Values{"username": {"u1"}}, nil)
	ck := sessionOf(w)
	if w.Code != 200 || ck == nil || !strings.Contains(w.Body.String(), `name="otp_code"`) {
		t.Fatalf("send: %d", w.Code)
	}
	code := otp()
	if w := do(e.h, "POST", "/portal/forgot/verify", url.Values{"otp_code": {code}}, ck); !strings.Contains(w.Body.String(), `name="npass"`) {
		t.Fatal("correct code not accepted")
	}
	if w := do(e.h, "POST", "/portal/forgot/reset", url.Values{"npass": {"newpw1"}, "cnpass": {"other"}}, ck); !strings.Contains(w.Body.String(), tr("Passwords does not match")) {
		t.Fatal("mismatch accepted")
	}
	if w := do(e.h, "POST", "/portal/forgot/reset", url.Values{"npass": {"newpw1"}, "cnpass": {"newpw1"}}, ck); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal/login" {
		t.Fatalf("reset: %d", w.Code)
	}
	if _, code := custLogin(t, e, "u1", "pw12345"); code == http.StatusSeeOther {
		t.Fatal("old password still works")
	}
	if c, code := custLogin(t, e, "u1", "newpw1"); code != http.StatusSeeOther || c == nil {
		t.Fatalf("new password: %d", code)
	}
}

func TestForgotWrongOTPLimited(t *testing.T) {
	e, otp := forgotApp(t)
	w := do(e.h, "POST", "/portal/forgot", url.Values{"username": {"u1"}}, nil)
	ck := sessionOf(w)
	real := otp()
	wrong := "000000"
	if real == wrong {
		wrong = "111111"
	}
	for i := 1; i <= 4; i++ {
		w := do(e.h, "POST", "/portal/forgot/verify", url.Values{"otp_code": {wrong}}, ck)
		if !strings.Contains(w.Body.String(), tr("Invalid Username or Verification Code")) {
			t.Fatalf("attempt %d not rejected", i)
		}
	}
	if w := do(e.h, "POST", "/portal/forgot/verify", url.Values{"otp_code": {wrong}}, ck); !strings.Contains(w.Body.String(), tr("Too many invalid attempts, please request a new Verification Code")) {
		t.Fatal("fifth wrong code not limited")
	}
	// the flow is dropped: even the right code no longer works
	if w := do(e.h, "POST", "/portal/forgot/verify", url.Values{"otp_code": {real}}, ck); strings.Contains(w.Body.String(), `name="npass"`) {
		t.Fatal("code still valid after limit")
	}
}

func TestForgotGatewayDisabled(t *testing.T) {
	e := billApp(t)
	w := do(e.h, "POST", "/portal/forgot", url.Values{"username": {"u1"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), tr("Password reset is not available, please contact admin")) || strings.Contains(w.Body.String(), `name="otp_code"`) {
		t.Fatalf("disabled gateway: %d", w.Code)
	}
}
