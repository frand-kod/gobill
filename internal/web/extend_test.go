package web

import (
	"net/url"
	"testing"
)

// R1: Agent/Sales may extend (PHP has no role check) but not deactivate; Report may do neither.
func TestExtendRoles(t *testing.T) {
	e := billApp(t)
	bulkAgent(t, e, "Agent")
	bulkAgent(t, e, "Report")
	p := e.plan(t, "day", "PPPoE", 10000)
	if err := e.s.Billing.Recharge(t.Context(), e.cust.ID, p.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	var id int64
	e.s.conn.QueryRow("SELECT id FROM subscriptions").Scan(&id)
	base := "/admin/subscriptions/" + itoa(id)
	wantCode(t, do(e.h, "POST", base+"/extend", url.Values{"days": {"3"}}, login(t, e.h, "Agent")), 303, "agent extend")
	wantCode(t, do(e.h, "POST", base+"/deactivate", nil, login(t, e.h, "Agent")), 403, "agent deactivate")
	wantCode(t, do(e.h, "POST", base+"/extend", url.Values{"days": {"3"}}, login(t, e.h, "Report")), 403, "report extend")
}
