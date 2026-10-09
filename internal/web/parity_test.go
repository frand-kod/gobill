package web

import (
	"net/url"
	"testing"
)

// P3: SuperAdmin/Admin may recharge a disabled plan, Agent/Sales may not.
func TestRechargeDisabledPlan(t *testing.T) {
	e := billApp(t)
	bulkAgent(t, e, "Agent")
	p := e.plan(t, "old", "PPPoE", 10000)
	if _, err := e.s.conn.Exec("UPDATE plans SET enabled = 0 WHERE id = ?", p.ID); err != nil {
		t.Fatal(err)
	}
	pay := "/admin/customers/" + itoa(e.cust.ID) + "/recharge"
	form := url.Values{"plan": {itoa(p.ID)}, "method": {"Cash"}}
	count := func() int {
		n := 0
		e.s.conn.QueryRow("SELECT COUNT(*) FROM subscriptions").Scan(&n)
		return n
	}
	wantCode(t, do(e.h, "POST", pay, form, login(t, e.h, "Agent")), 303, "agent")
	if count() != 0 {
		t.Fatal("agent recharged a disabled plan")
	}
	wantCode(t, do(e.h, "POST", pay, form, e.c), 303, "admin")
	if count() != 1 {
		t.Fatal("admin could not recharge a disabled plan")
	}
}
