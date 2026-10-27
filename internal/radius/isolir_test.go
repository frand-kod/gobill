package radius

import (
	"context"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/secret"
)

// An isolated customer, with no active subscription, gets the isolir profile and the Mikrotik-Address-List
// while the isolation runs. A normal customer gets no address-list. An isolation that ended or ran out
// rejects the login like an expired plan.
func TestIsolirAccessAcceptCarriesAddressList(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	if _, err := isolir.EnsurePlan(ctx, e.q); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "isolir_enabled", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	pw, _ := secret.Seal(e.s.Key, []byte("pw"))
	c, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "iso", PasswordHash: "h", Fullname: "I", ServiceType: "Hotspot", SecretEnc: pw, AutoRenewal: 0, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	iso, err := e.q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: c.ID, StartedAt: e.now.Unix(), EndsAt: e.now.Unix() + 86400})
	if err != nil {
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

	// the isolation ran out: the login is refused, as for any expired plan
	e.now = e.now.Add(2 * 86400 * time.Second)
	if r := e.auth(t, pap("iso", "pw")); r.Code == radius.CodeAccessAccept {
		t.Fatal("expired isolation still accepted")
	}

	// an ended isolation (a plan bought or switched off) refuses the same way
	e.now = time.Unix(iso.StartedAt, 0)
	if _, err := e.q.EndIsolation(ctx, db.EndIsolationParams{ID: iso.ID, Now: e.now.Unix(), Reason: "paid"}); err != nil {
		t.Fatal(err)
	}
	if r := e.auth(t, pap("iso", "pw")); r.Code == radius.CodeAccessAccept {
		t.Fatal("ended isolation still accepted")
	}
}
