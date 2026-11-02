package web

import (
	"net/url"
	"strings"
	"testing"
)

// L-1: the clean-logs confirm names the days; the dialog fills {keep} from the input.
func TestCleanLogsConfirmNamesDays(t *testing.T) {
	e := englishApp(t)
	body := do(e.h, "GET", "/admin/logs", nil, e.c).Body.String()
	if !strings.Contains(body, `data-confirm="Delete logs older than {keep} days?"`) {
		t.Error("clean logs confirm does not carry the {keep} placeholder")
	}
}

// L-5: the customer form has a tel phone input, a password toggle and the Generate button.
func TestCustomerFormPhoneAndPassword(t *testing.T) {
	e := billApp(t)
	body := do(e.h, "GET", "/admin/customers/new", nil, e.c).Body.String()
	for _, want := range []string{`name="phone" type="tel"`, `inputmode="tel" autocomplete="tel"`, `data-pw="password"`, `>Buat</button>`} {
		if !strings.Contains(body, want) {
			t.Errorf("customer form missing %q", want)
		}
	}
}

// L-5: a decimal where a whole number belongs gets its own message, for the customer day and plan price.
func TestDecimalFieldsGetWholeNumberMessage(t *testing.T) {
	const msg = "Gunakan angka bulat tanpa titik/koma"
	e := billApp(t)
	cust := url.Values{"username": {"newc"}, "fullname": {"New"}, "password": {"Secret-pass-1"}, "status": {"Active"},
		"service_type": {"Hotspot"}, "billing_day": {"1.5"}}
	w := do(e.h, "POST", "/admin/customers", cust, e.c)
	wantCode(t, w, 422, "billing day with decimal")
	if !strings.Contains(w.Body.String(), msg) {
		t.Errorf("billing day: no %q message", msg)
	}
	plan := url.Values{"name": {"Dec"}, "type": {"Hotspot"}, "billing": {"prepaid"}, "price": {"12.5"}, "validity": {"1"},
		"validity_unit": {"Days"}, "bandwidth_id": {itoa(e.bw)}, "device": {"Radius"}, "enabled": {"1"}}
	w = do(e.h, "POST", "/admin/plans", plan, e.c)
	wantCode(t, w, 422, "plan price with decimal")
	if !strings.Contains(w.Body.String(), msg) {
		t.Errorf("plan price: no %q message", msg)
	}
}

// L-6: Recharge Zero shows the Rp 0 warning; a paid method does not.
func TestRechargeZeroWarning(t *testing.T) {
	const warn = "Dicatat Rp 0, tidak ada pemasukan"
	e := billApp(t)
	gold := e.plan(t, "gold", "PPPoE", 10000)
	form := url.Values{"customer": {"u1"}, "plan": {itoa(gold.ID)}}
	form.Set("method", "Zero")
	if w := do(e.h, "POST", "/admin/recharge", form, e.c); !strings.Contains(w.Body.String(), warn) {
		t.Errorf("Recharge Zero has no warning")
	}
	form.Set("method", "Cash")
	if w := do(e.h, "POST", "/admin/recharge", form, e.c); strings.Contains(w.Body.String(), warn) {
		t.Errorf("Cash recharge shows the Rp 0 warning")
	}
}

// L-9: a replayed deposit goes back to the form with the customer still chosen.
func TestDepositReplayKeepsCustomer(t *testing.T) {
	e := billApp(t)
	w := do(e.h, "POST", "/admin/deposit", url.Values{"customer": {"u1"}, "amount": {"1000"}, "confirmed": {"1"}, "token": {"unknown"}}, e.c)
	wantCode(t, w, 303, "replayed deposit")
	if loc := w.Header().Get("Location"); loc != "/admin/deposit?customer=u1" {
		t.Errorf("replay redirect = %q, want the customer kept", loc)
	}
}
