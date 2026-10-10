package web

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// An admin can fix a customer's username; the router account is renamed before the sync,
// history keeps the old name, and a name used by another customer is refused.
func TestEditCustomerUsername(t *testing.T) {
	e := billApp(t)
	e.recordDevice()
	p := e.plan(t, "Gold", "PPPoE", 10000)
	if err := e.s.Billing.Recharge(t.Context(), e.cust.ID, p.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	last, err := e.q.ListTransactions(t.Context(), db.ListTransactionsParams{Limit: 1})
	if err != nil || len(last) != 1 || !regexp.MustCompile(`^INV-\d{4}-\d{6}$`).MatchString(last[0].Invoice) {
		t.Fatalf("recharge invoice: %+v %v", last, err)
	}
	invoice := last[0].Invoice
	other, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "other", PasswordHash: "h", Fullname: "O", ServiceType: "PPPoE", PppoeUsername: "other-ppp", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	post := func(user, ppp string) int {
		form := url.Values{"username": {user}, "fullname": {"U One"}, "service_type": {"PPPoE"}, "status": {"Active"}, "pppoe_username": {ppp}}
		return do(e.h, "POST", "/admin/customers/"+itoa(e.cust.ID), form, e.c).Code
	}
	for _, c := range []struct{ user, ppp string }{{"other", ""}, {"other-ppp", ""}, {"u1", "other"}} {
		if code := post(c.user, c.ppp); code != 422 {
			t.Fatalf("%+v: code %d, want 422", c, code)
		}
	}
	if got, _ := e.q.GetCustomer(t.Context(), e.cust.ID); got.Username != "u1" {
		t.Fatalf("clash must change nothing, username %q", got.Username)
	}
	*e.calls = nil
	if code := post("u2", ""); code != 303 {
		t.Fatalf("rename: %d", code)
	}
	if got, _ := e.q.GetCustomer(t.Context(), e.cust.ID); got.Username != "u2" {
		t.Fatalf("username %q", got.Username)
	}
	if got := strings.Join(*e.calls, ","); got != "rename u1>u2,add u2" {
		t.Fatalf("router calls: %q", got)
	}
	if trx, err := e.q.GetTransactionByInvoice(t.Context(), invoice); err != nil || trx.Username != "u1" {
		t.Fatalf("history must keep the old username: %+v %v", trx, err)
	}
	// pppoe_username change renames the PPPoE secret from the old effective name
	*e.calls = nil
	if code := post("u2", "ppp2"); code != 303 {
		t.Fatalf("pppoe: %d", code)
	}
	if got := strings.Join(*e.calls, ","); got != "rename u2>ppp2,add u2" {
		t.Fatalf("pppoe calls: %q", got)
	}
	_ = other
}
