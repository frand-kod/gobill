package web

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestRechargePage(t *testing.T) {
	e := billApp(t)
	gold := e.plan(t, "gold", "PPPoE", 10000)
	if _, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "off", PasswordHash: "h", Fullname: "Off", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Inactive"}); err != nil {
		t.Fatal(err)
	}
	rita := login(t, e.h, "rita")

	// roles: staff may open the page, Report may not
	wantCode(t, do(e.h, "GET", "/admin/recharge", nil, e.c), 200, "page for SuperAdmin")
	wantCode(t, do(e.h, "GET", "/admin/recharge", nil, rita), 403, "page for Report")
	wantCode(t, do(e.h, "POST", "/admin/recharge", url.Values{"customer": {"u1"}}, rita), 403, "submit for Report")

	// prefill and the plan and method lists
	w := do(e.h, "GET", "/admin/recharge?customer=u1", nil, e.c)
	if b := w.Body.String(); !strings.Contains(b, `value="u1"`) || !strings.Contains(b, `value="`+itoa(gold.ID)+`"`) || !strings.Contains(b, `value="Balance"`) {
		t.Fatalf("prefill and lists missing: %s", b)
	}

	// valid username: the existing confirm page, and nothing is written
	cust := sql.NullInt64{Int64: e.cust.ID, Valid: true}
	before, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: cust, Limit: 10})
	w = do(e.h, "POST", "/admin/recharge", url.Values{"customer": {"u1"}, "plan": {itoa(gold.ID)}, "method": {"Cash"}}, e.c)
	if b := w.Body.String(); w.Code != 200 || !strings.Contains(b, `action="/admin/customers/`+itoa(e.cust.ID)+`/recharge"`) {
		t.Fatalf("confirm page: %d\n%s", w.Code, b)
	}
	after, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: cust, Limit: 10})
	subs, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 5})
	if len(after) != len(before) || len(subs) != 0 || len(*e.calls) != 0 {
		t.Fatalf("preview wrote data: trx %d->%d subs %d calls %v", len(before), len(after), len(subs), *e.calls)
	}

	// field errors, all rendered on the same page
	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"unknown customer", url.Values{"customer": {"nobody"}, "plan": {itoa(gold.ID)}, "method": {"Cash"}}, "Pelanggan tidak ditemukan"},
		{"empty customer", url.Values{"customer": {""}, "plan": {itoa(gold.ID)}, "method": {"Cash"}}, "Pilih pelanggan dulu"},
		{"inactive customer", url.Values{"customer": {"off"}, "plan": {itoa(gold.ID)}, "method": {"Cash"}}, "Pelanggan ini tidak aktif"},
		{"no plan", url.Values{"customer": {"u1"}, "method": {"Cash"}}, "Pilih paket layanan dulu"},
		{"bad plan", url.Values{"customer": {"u1"}, "plan": {"999"}, "method": {"Cash"}}, "Paket tidak ditemukan"},
		{"bad method", url.Values{"customer": {"u1"}, "plan": {itoa(gold.ID)}, "method": {"Crypto"}}, "Metode pembayaran tidak valid"},
	}
	for _, c := range cases {
		w := do(e.h, "POST", "/admin/recharge", c.form, e.c)
		if w.Code != 422 || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: got %d, want 422 with %q", c.name, w.Code, c.want)
		}
	}
	if len(*e.calls) != 0 {
		t.Fatalf("errors must not touch the router: %v", *e.calls)
	}
}
