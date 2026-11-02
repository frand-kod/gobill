package web

import (
	"net/url"
	"strings"
	"testing"
)

// A-M1: postpaid needs a billing day; the form marks it required; a schedule error says which part is wrong.
func TestPlanBillingDayAndScheduleErrors(t *testing.T) {
	e := billApp(t)
	f := url.Values{"name": {"Post"}, "type": {"Hotspot"}, "billing": {"postpaid"}, "price": {"1000"}, "validity": {"1"},
		"validity_unit": {"Days"}, "bandwidth_id": {itoa(e.bw)}, "device": {"Radius"}, "enabled": {"1"}}
	w := do(e.h, "POST", "/admin/plans", f, e.c)
	wantCode(t, w, 422, "postpaid without day")
	if !strings.Contains(w.Body.String(), `id="billing_day-err"`) {
		t.Fatal("no billing day error")
	}
	f.Set("billing_day", "5")
	wantCode(t, do(e.h, "POST", "/admin/plans", f, e.c), 303, "postpaid with day")
	f.Set("name", "Post2")
	f.Set("login_window", "Funday 08:00-17:00")
	if w := do(e.h, "POST", "/admin/plans", f, e.c); !strings.Contains(w.Body.String(), "Nama hari tidak dikenal") {
		t.Fatalf("day error not specific: %d", w.Code)
	}
	if w := do(e.h, "GET", "/admin/plans/new", nil, e.c); !strings.Contains(w.Body.String(), `:aria-required=`) {
		t.Fatal("no conditional aria-required")
	}
}

// A-M8: a generated secret that fails validation is kept in the re-rendered form.
func TestNASSecretKeptOn422(t *testing.T) {
	_, h, _, c := crudApp(t)
	w := do(h, "POST", "/admin/nas", url.Values{"name": {"n"}, "ip": {"bad"}, "secret": {"Xk3-9fQ_veryStrongSecret"}}, c)
	wantCode(t, w, 422, "bad ip")
	if !strings.Contains(w.Body.String(), `value="Xk3-9fQ_veryStrongSecret"`) {
		t.Fatal("secret wiped")
	}
}
