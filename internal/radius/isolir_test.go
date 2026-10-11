package radius

import (
	"context"
	"testing"

	"layeh.com/radius"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/secret"
)

// An isolated customer gets the isolir plan's rate limit and the Mikrotik-Address-List; a normal one none.
func TestIsolirAccessAcceptCarriesAddressList(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	isoID, err := isolir.EnsurePlan(ctx, e.q)
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := secret.Seal(e.s.Key, []byte("pw"))
	c, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "iso", PasswordHash: "h", Fullname: "I", ServiceType: "Hotspot", SecretEnc: pw, AutoRenewal: 0, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: isoID, Type: "Hotspot",
		StartedAt: e.now.Unix(), ExpiresAt: e.now.Unix() + 86400}); err != nil {
		t.Fatal(err)
	}

	r := e.auth(t, pap("iso", "pw"))
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("isolated code %v", r.Code)
	}
	if got := string(mikrotik.MikrotikAddressList_Get(r)); got != isolir.DefaultAddrList {
		t.Fatalf("address-list %q, want %q", got, isolir.DefaultAddrList)
	}
	if rate := string(mikrotik.MikrotikRateLimit_Get(r)); rate == "" {
		t.Fatal("isolir plan has no rate limit")
	}

	// a normal customer carries no address-list
	if r := e.auth(t, pap("alice", "pw")); r.Code != radius.CodeAccessAccept || len(mikrotik.MikrotikAddressList_Get(r)) != 0 {
		t.Fatalf("normal customer: code %v list %q", r.Code, mikrotik.MikrotikAddressList_Get(r))
	}
}
