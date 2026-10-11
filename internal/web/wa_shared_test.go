package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// forgotAppRaw is forgotApp, but it returns the whole last message the gateway got, not just a code.
func forgotAppRaw(t *testing.T) (*billEnv, func() string) {
	t.Helper()
	e := billApp(t)
	gw, last := fakeGateway(t)
	if err := e.q.UpdateCustomer(t.Context(), db.UpdateCustomerParams{Fullname: e.cust.Fullname, Phone: "081234567890",
		ServiceType: e.cust.ServiceType, AutoRenewal: 1, Status: "Active", ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "sms_url", Value: gw.URL + "/?to=[number]&text=[text]"}); err != nil {
		t.Fatal(err)
	}
	return e, last
}

// addSharedNumberCustomer makes u2 on the same WhatsApp number as u1 (the forgotApp customer).
func addSharedNumberCustomer(t *testing.T, e *billEnv, phone string) db.Customer {
	t.Helper()
	c, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Istri",
		ServiceType: "Hotspot", Phone: phone, AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The setting is No by default, is shown as the selected value, and only Yes or No is accepted.
func TestWAUniqueSettingValues(t *testing.T) {
	e := billApp(t)
	c := login(t, e.h, "alice")
	body := getBody(t, e.h, c, "/admin/settings/billing")
	if !strings.Contains(selectBlock(body, "wa_unique_per_account"), `<option value="no" selected>`) {
		t.Fatal("unset setting does not show No")
	}
	if w := do(e.h, "POST", "/admin/settings/billing", url.Values{"wa_unique_per_account": {"yes"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("save yes: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(selectBlock(getBody(t, e.h, c, "/admin/settings/billing"), "wa_unique_per_account"), `<option value="yes" selected>`) {
		t.Fatal("saved Yes not shown")
	}
	if w := do(e.h, "POST", "/admin/settings/billing", url.Values{"wa_unique_per_account": {"maybe"}}, c); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad value: %d", w.Code)
	}
}

// Default (No): a second account may register with a number another account uses. Yes: it is refused.
func TestWAUniqueRegistrationBothValues(t *testing.T) {
	e := billApp(t)
	if err := e.q.UpdateCustomer(t.Context(), db.UpdateCustomerParams{Fullname: e.cust.Fullname, Phone: "081234567890",
		ServiceType: e.cust.ServiceType, AutoRenewal: 1, Status: "Active", ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	if w := regForm(e, "istri", "abc12345", "0812-3456-7890"); w.Code != http.StatusSeeOther {
		t.Fatalf("shared number refused by default: %d", w.Code)
	}
	if _, err := e.q.GetCustomerByUsername(t.Context(), "istri"); err != nil {
		t.Fatal("shared-number account not created")
	}
	setting(t, e, "wa_unique_per_account", "yes")
	w := regForm(e, "anak", "abc12345", "081234567890")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "sudah dipakai akun lain") {
		t.Fatalf("yes: %d", w.Code)
	}
	noCustomer(t, e, "anak")
}

// Forgot username with two accounts on one number sends both usernames.
func TestForgotUsernameListsEveryAccountOnNumber(t *testing.T) {
	e, last := forgotAppRaw(t)
	addSharedNumberCustomer(t, e, "081234567890")
	do(e.h, "POST", "/portal/forgot/username", url.Values{"contact": {"081234567890"}}, nil)
	var text string
	for i := 0; i < 100 && !strings.Contains(text, "u1"); i++ {
		text = last()
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(text, "u1") || !strings.Contains(text, "u2") {
		t.Fatalf("usernames sent: %q, want u1 and u2", text)
	}
}

// Forgot password asks for the username, so the code goes to that account's number and only that
// account's password changes, even when another account shares the number.
func TestForgotPasswordWithSharedNumber(t *testing.T) {
	e, otp := forgotApp(t)
	istri := addSharedNumberCustomer(t, e, "081234567890")
	before, _ := e.q.GetCustomerByUsername(t.Context(), "u1")
	w := do(e.h, "POST", "/portal/forgot", url.Values{"username": {"u2"}}, nil)
	ck := sessionOf(w)
	if ck == nil || !strings.Contains(w.Body.String(), `name="otp_code"`) {
		t.Fatalf("forgot for u2: %d", w.Code)
	}
	code := otp()
	do(e.h, "POST", "/portal/forgot/verify", url.Values{"otp_code": {code}}, ck)
	if r := do(e.h, "POST", "/portal/forgot/reset", url.Values{"npass": {"newpas7"}, "cnpass": {"newpas7"}}, ck); r.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d %s", r.Code, r.Body.String())
	}
	after, _ := e.q.GetCustomerByUsername(t.Context(), "u1")
	if after.PasswordHash != before.PasswordHash {
		t.Error("u1's password changed by u2's reset")
	}
	if got, _ := e.q.GetCustomerByUsername(t.Context(), "u2"); got.PasswordHash == istri.PasswordHash {
		t.Error("u2's password not changed")
	}
}
