package web

import (
	"net/url"
	"testing"
)

// Audit #1: a resubmitted deposit form (double click, back+resend) must credit once.
func TestAuditMoney1_DepositDoubleSubmit(t *testing.T) {
	e := billApp(t)
	form := url.Values{"customer": {e.cust.Username}, "plan": {""}, "amount": {"5000"}, "token": {newFormToken()}}
	do(e.h, "POST", "/admin/deposit", form, e.c)
	do(e.h, "POST", "/admin/deposit", form, e.c)
	if c, _ := e.q.GetCustomer(t.Context(), e.cust.ID); c.Balance != 5000 {
		t.Fatalf("one deposit form submitted twice: balance %d", c.Balance)
	}
}

// Audit #1: a resubmitted single-customer recharge must charge and extend once.
func TestAuditMoney1_RechargeDoubleSubmit(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	form := url.Values{"plan": {itoa(p.ID)}, "method": {"Cash"}, "token": {newFormToken()}}
	path := "/admin/customers/" + itoa(e.cust.ID) + "/recharge"
	do(e.h, "POST", path, form, e.c)
	do(e.h, "POST", path, form, e.c)
	var n int
	e.s.conn.QueryRow(`SELECT COUNT(*) FROM transactions WHERE customer_id = ?`, e.cust.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("one recharge form submitted twice: %d transactions", n)
	}
}
