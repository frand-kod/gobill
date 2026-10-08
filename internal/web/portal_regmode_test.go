package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestPortalRegistrationModes(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "Gold", "PPPoE", 10000)
	e.q.CreateVoucher(t.Context(), db.CreateVoucherParams{Code: "VCH1", PlanID: p.ID})
	reg := url.Values{"username": {"newbie"}, "fullname": {"New Bie"}, "password": {"abc123"}, "cpassword": {"abc123"}}
	act := func(user, code string) *httptest.ResponseRecorder {
		return do(e.h, "POST", "/portal/login/activation", url.Values{"username": {user}, "voucher": {code}}, nil)
	}
	// no: normal registration works, voucher login refused
	setting(t, e, "disable_registration", "no")
	if w := do(e.h, "GET", "/portal/login", nil, nil); !strings.Contains(w.Body.String(), "/portal/register") || strings.Contains(w.Body.String(), "activation") {
		t.Fatal("login page in open mode")
	}
	if w := act("vuser", "VCH1"); w.Header().Get("Location") != "/portal/login" {
		t.Fatalf("activation while open: %d", w.Code)
	}
	if _, err := e.q.GetCustomerByUsername(t.Context(), "vuser"); err == nil {
		t.Fatal("voucher account created while open")
	}
	if w := do(e.h, "POST", "/portal/register", reg, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("register: %d", w.Code)
	}
	// noreg: register page and link refused
	setting(t, e, "disable_registration", "noreg")
	if w := do(e.h, "GET", "/portal/register", nil, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("noreg page: %d", w.Code)
	}
	if w := do(e.h, "GET", "/portal/login", nil, nil); strings.Contains(w.Body.String(), "/portal/register") {
		t.Fatal("register link shown in noreg")
	}
	// yes: voucher only
	setting(t, e, "disable_registration", "yes")
	if w := do(e.h, "GET", "/portal/register", nil, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("voucher-only register page: %d", w.Code)
	}
	if w := do(e.h, "GET", "/portal/login", nil, nil); strings.Contains(w.Body.String(), "/portal/register") || !strings.Contains(w.Body.String(), "activation") {
		t.Fatal("login page in voucher mode")
	}
	if w := act("vuser", "NOPE"); w.Code != 200 {
		t.Fatalf("bad voucher: %d", w.Code)
	}
	if _, err := e.q.GetCustomerByUsername(t.Context(), "vuser"); err == nil {
		t.Fatal("customer created for invalid voucher")
	}
	if w := act("vuser", "VCH1"); w.Code != http.StatusSeeOther {
		t.Fatalf("voucher: %d", w.Code)
	}
	c, err := e.q.GetCustomerByUsername(t.Context(), "vuser")
	if err != nil || len(c.SecretEnc) == 0 {
		t.Fatalf("customer: %v", err)
	}
	if subs, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 5}); len(subs) != 1 {
		t.Fatalf("subs %d", len(subs))
	}
	if cc, _ := custLogin(t, e, "vuser", "VCH1"); cc == nil {
		t.Fatal("cannot log in with voucher code")
	}
	if w := act("other", "VCH1"); w.Code != 200 {
		t.Fatalf("used voucher: %d", w.Code)
	}
}
