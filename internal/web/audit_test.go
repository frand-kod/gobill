package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/frand-kod/gobill/internal/wg"
)

// Lifecycle F3: banning a customer with a live router plan leaves them on the router.
func TestAuditLifecycle3_BanNoRouterRemoval(t *testing.T) {
	e := billApp(t)
	e.recordDevice()
	p := e.plan(t, "Gold", "PPPoE", 10000)
	if err := e.s.Billing.Recharge(t.Context(), e.cust.ID, p.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	*e.calls = nil
	form := url.Values{"username": {"u1"}, "fullname": {"U One"}, "service_type": {"PPPoE"}, "status": {"Banned"}}
	if code := do(e.h, "POST", "/admin/customers/"+itoa(e.cust.ID), form, e.c).Code; code != 303 {
		t.Fatalf("save: %d", code)
	}
	if len(*e.calls) == 0 {
		t.Fatal("banned customer not removed/disabled on the router")
	}
}

// Router #1: provisioning switches the stored API host/user/password before the router ran the script.
func TestAuditRouter1_WGProvisionEarlyCredSwitch(t *testing.T) {
	if !wg.Built {
		t.Skip("nowg build")
	}
	_, h, q, c := wgSetup(t)
	wgSave(h, c, map[string]string{"wg_enabled": "yes", "wg_port": "38219", "wg_subnet": "10.79.0.0/24", "wg_endpoint": "vpn.example.com"})
	r := wgRouter(t, q, "alpha")
	if w := wgPost(h, c, r.ID, "provision"); w.Code != http.StatusSeeOther {
		t.Fatalf("provision: %d", w.Code)
	}
	got, _ := q.GetRouter(t.Context(), r.ID)
	if got.Host != r.Host || string(got.PasswordEnc) != string(r.PasswordEnc) {
		t.Fatalf("router creds switched to %s/%s before the script was fetched", got.Host, got.Username)
	}
}
