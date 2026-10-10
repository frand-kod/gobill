package web

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The customer picker reads /admin/search with an empty or non-empty q and a limit (capped at 20).
func TestCustomerPickerSearch(t *testing.T) {
	_, h, q, c := crudApp(t)
	type row struct{ Username, Status, Balance string }
	get := func(target string) []row {
		t.Helper()
		w := do(h, "GET", target, nil, c)
		wantCode(t, w, 200, target)
		var out []row
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: json: %v: %s", target, err, w.Body)
		}
		return out
	}

	newCust(t, q, "zz9", "Disabled One", "0811", "", "Disabled")
	newCust(t, q, "act01", "Budi", "0812", "", "Active")
	for i := 1; i <= 25; i++ {
		newCust(t, q, fmt.Sprintf("bulk%02d", i), "Bulk User", "0899", "", "Active")
	}

	// empty q: first 20, Active first then username; the disabled one is left out of the first 20
	r := get("/admin/search?q=")
	if len(r) != 20 {
		t.Fatalf("empty q: got %d rows, want 20", len(r))
	}
	if r[0].Username != "act01" || r[1].Username != "bulk01" || r[19].Username != "bulk19" {
		t.Errorf("empty q order: %+v", r)
	}
	for _, x := range r {
		if x.Status != "Active" {
			t.Errorf("empty q: non-active %q ahead of the list", x.Username)
		}
	}
	if n := len(get("/admin/search")); n != 20 {
		t.Errorf("missing q: got %d rows, want 20", n)
	}

	// limit: capped at 20, honoured below that, and a search keeps its default of 8
	if n := len(get("/admin/search?q=&limit=50")); n != 20 {
		t.Errorf("limit=50: got %d rows, want 20", n)
	}
	if n := len(get("/admin/search?q=&limit=3")); n != 3 {
		t.Errorf("limit=3: got %d rows, want 3", n)
	}
	if n := len(get("/admin/search?q=bulk&limit=50")); n != 20 {
		t.Errorf("search limit=50: got %d rows, want 20", n)
	}
	if n := len(get("/admin/search?q=bulk")); n != 8 {
		t.Errorf("search default: got %d rows, want 8", n)
	}
	if n := len(get("/admin/search?q=bulk&limit=junk")); n != 8 {
		t.Errorf("bad limit: got %d rows, want 8", n)
	}

	// the picker payload carries the balance formatted
	if r := get("/admin/search?q=act01&limit=1"); len(r) != 1 || r[0].Balance != money(0) {
		t.Errorf("picker row: %+v", r)
	}

	// a disabled customer shows up in an empty list when nothing else is there
	_, h2, q2, c2 := crudApp(t)
	newCust(t, q2, "zz9", "Disabled One", "0811", "", "Disabled")
	newCust(t, q2, "act01", "Budi", "0812", "", "Active")
	w := do(h2, "GET", "/admin/search?q=", nil, c2)
	var both []row
	json.Unmarshal(w.Body.Bytes(), &both)
	if len(both) != 2 || both[0].Username != "act01" || both[1].Status != "Disabled" {
		t.Errorf("active first: %+v", both)
	}

	// role check unchanged: staff only
	wantCode(t, do(h, "GET", "/admin/search?q=", nil, login(t, h, "rita")), 403, "Report role")
	wantCode(t, do(h, "GET", "/admin/search?q=", nil, nil), 303, "anonymous")
}

// The three single-customer pages render the picker with the username field name and prefill.
func TestCustomerPickerPages(t *testing.T) {
	_, h, q, c := crudApp(t)
	newCust(t, q, "zed01", "Zed Smith", "081234", "", "Active")
	for _, p := range []string{"/admin/recharge?customer=zed01", "/admin/deposit?customer=zed01", "/admin/vouchers/redeem?customer=zed01"} {
		w := do(h, "GET", p, nil, c)
		wantCode(t, w, 200, p)
		b := w.Body.String()
		for _, want := range []string{
			`/static/customer-pick.js`,
			`x-data="gbCustomerPick(`,
			`<input type="hidden" name="customer"`,                         // the value the handlers read
			`<noscript><input class="input" id="customer" name="customer"`, // no-JS fallback
			`role="combobox"`, `role="listbox"`,
			`zed01`, // prefilled username in the picker config
		} {
			if !strings.Contains(b, want) {
				t.Errorf("%s: missing %q", p, want)
			}
		}
	}
	// deposit shows balances in the picker; the others do not
	if b := do(h, "GET", "/admin/deposit", nil, c).Body.String(); !strings.Contains(b, `balance&#34;:true`) {
		t.Error("deposit picker must show balances")
	}
	if b := do(h, "GET", "/admin/recharge", nil, c).Body.String(); strings.Contains(b, `balance&#34;:true`) {
		t.Error("recharge picker must not show balances")
	}
	// a POST with the field name still goes through the existing handler (unknown user -> error on the page)
	form := url.Values{"customer": {"nobody"}, "plan": {""}, "method": {"Cash"}}
	if b := do(h, "POST", "/admin/recharge", form, c).Body.String(); !strings.Contains(b, `id="customer-err"`) {
		t.Error("recharge POST must read the customer field")
	}
}
