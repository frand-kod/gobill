package radius

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/secret"
)

// vsa returns the integer of vendor sub-attribute typ, ok=false when absent.
func vsa(p *radius.Packet, vendor uint32, typ byte) (uint32, bool) {
	for _, a := range p.Attributes {
		if a.Type == rfc2865.VendorSpecific_Type && len(a.Attribute) == 10 &&
			binary.BigEndian.Uint32(a.Attribute) == vendor && a.Attribute[4] == typ {
			return binary.BigEndian.Uint32(a.Attribute[6:]), true
		}
	}
	return 0, false
}

func setNASType(t *testing.T, e *env, typ string) {
	t.Helper()
	rows, err := e.q.ListNAS(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range rows {
		if err := e.q.SetNASType(t.Context(), db.SetNASTypeParams{Type: typ, ID: n.ID}); err != nil {
			t.Fatal(err)
		}
	}
	e.s.forgetNAS()
}

func TestRateBPS(t *testing.T) {
	for in, want := range map[string][2]uint32{
		"512K/5M":         {512000, 5000000},
		"512K/5M 10M/10M": {512000, 5000000},
		"10M/1G":          {10000000, 1000000000},
		"9000M/9000M":     {4294967295, 4294967295}, // past 32 bits: the most WISPr can carry
	} {
		up, down, ok := rateBPS(in)
		if !ok || up != want[0] || down != want[1] {
			t.Errorf("%q: %d %d %v, want %v", in, up, down, ok, want)
		}
	}
	for _, in := range []string{"", "5M", "x/5M", "0/5M"} {
		if _, _, ok := rateBPS(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

// A wispr NAS gets WISPr-Bandwidth-Max-Up/Down in bit/s and no MikroTik attribute; a standard NAS gets
// neither; an isolated customer is rejected on both, since only MikroTik has the isolir address list.
func TestNASTypeAttributes(t *testing.T) {
	ctx := context.Background()
	e := setup(t)

	setNASType(t, e, NASWISPr)
	r := e.auth(t, pap("alice", "pw"))
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("code %v", r.Code)
	}
	up, okU := vsa(r, vendorWISPr, wisprBandwidthMaxUp)
	down, okD := vsa(r, vendorWISPr, wisprBandwidthMaxDn)
	if !okU || !okD || up != 512000 || down != 5000000 {
		t.Fatalf("WISPr up %d %v down %d %v", up, okU, down, okD)
	}
	if len(mikrotik.MikrotikRateLimit_Get(r)) != 0 {
		t.Fatal("wispr NAS got Mikrotik-Rate-Limit")
	}

	setNASType(t, e, NASStandard)
	r = e.auth(t, pap("alice", "pw"))
	if r.Code != radius.CodeAccessAccept || r.Get(rfc2865.VendorSpecific_Type) != nil {
		t.Fatalf("standard NAS: code %v, vendor attribute present", r.Code)
	}

	// isolated customer
	if _, err := isolir.EnsurePlan(ctx, e.q); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "isolir_enabled", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	pw, _ := secret.Seal(e.s.Key, []byte("pw"))
	c, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "iso", PasswordHash: "h", Fullname: "I", ServiceType: "Hotspot", SecretEnc: pw, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: c.ID, StartedAt: e.now.Unix(), EndsAt: e.now.Add(24 * time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{NASWISPr, NASStandard} {
		setNASType(t, e, typ)
		if r := e.auth(t, pap("iso", "pw")); r.Code != radius.CodeAccessReject || rfc2865.ReplyMessage_GetString(r) != msgIsolated {
			t.Fatalf("%s: isolated customer got %v", typ, r.Code)
		}
	}
	setNASType(t, e, NASMikrotik)
	if r := e.auth(t, pap("iso", "pw")); r.Code != radius.CodeAccessAccept {
		t.Fatalf("mikrotik: isolated customer got %v", r.Code)
	}
}

// CoA to a wispr NAS carries the speed as WISPr; CoovaChilli needs User-Name in it.
func TestCoAWISPr(t *testing.T) {
	e := setup(t)
	sealed, _ := secret.Seal(e.s.Key, nasSecret)
	if _, err := e.q.CreateNAS(t.Context(), db.CreateNASParams{Name: "lo", Ip: "127.0.0.1", SecretEnc: sealed}); err != nil {
		t.Fatal(err)
	}
	setNASType(t, e, NASWISPr)
	port, got := fakeNAS(t, radius.CodeCoAACK)
	sess := db.RadiusSession{Username: "alice", SessionID: "s1", NasIp: "127.0.0.1"}
	if err := CoA(t.Context(), e.q, e.s.Key, port, sess, "", e.now); err != nil {
		t.Fatal(err)
	}
	p, err := radius.Parse(<-got, nasSecret)
	if err != nil {
		t.Fatal(err)
	}
	if rfc2865.UserName_GetString(p) != "alice" || len(mikrotik.MikrotikRateLimit_Get(p)) != 0 {
		t.Fatal("CoA lacks User-Name or carries Mikrotik-Rate-Limit")
	}
	if down, ok := vsa(p, vendorWISPr, wisprBandwidthMaxDn); !ok || down != 5000000 {
		t.Fatalf("CoA WISPr down %d %v", down, ok)
	}
}
