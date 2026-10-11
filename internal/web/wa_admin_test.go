package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The admin edit form follows the same rule: a shared number is saved by default, refused with Yes.
func TestWAUniqueAdminEdit(t *testing.T) {
	e := billApp(t)
	addSharedNumberCustomer(t, e, "081234567890")
	form := url.Values{"username": {"u1"}, "fullname": {"U One"}, "service_type": {"Hotspot"}, "status": {"Active"}, "phone": {"081234567890"}}
	if w := do(e.h, "POST", "/admin/customers/"+itoa(e.cust.ID), form, e.c); w.Code != http.StatusSeeOther {
		t.Fatalf("shared number refused by default: %d %s", w.Code, w.Body.String())
	}
	setting(t, e, "wa_unique_per_account", "yes")
	w := do(e.h, "POST", "/admin/customers/"+itoa(e.cust.ID), form, e.c)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "sudah dipakai akun lain") {
		t.Fatalf("yes: %d", w.Code)
	}
}
