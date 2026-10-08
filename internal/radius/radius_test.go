package radius

import (
	"context"
	"crypto/md5"
	"database/sql"
	"net"
	"path/filepath"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/vendors/microsoft"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

var nasSecret = []byte("nas-secret")

type rec struct{ p *radius.Packet }

func (r *rec) Write(p *radius.Packet) error { r.p = p; return nil }

type env struct {
	s       *Server
	q       *db.Queries
	now     time.Time
	trusted bool
}

// setup: NAS 10.0.0.1, customer "alice" (password "pw") with an active Hotspot sub expiring in 1h.
func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	key := make([]byte, 32)
	e := &env{q: q, now: time.Unix(1_000_000, 0), trusted: true}
	e.s = &Server{Q: q, Key: key, Now: func() time.Time { return e.now }, Trusted: func() bool { return e.trusted }}

	sealed, _ := secret.Seal(key, nasSecret)
	if _, err := q.CreateNAS(ctx, db.CreateNASParams{Name: "n1", Ip: "10.0.0.0/24", SecretEnc: sealed}); err != nil {
		t.Fatal(err)
	}
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 5, RateDownUnit: "Mbps", RateUp: 512, RateUpUnit: "Kbps", Burst: "10M/10M"})
	rt, _ := q.CreateRouter(ctx, db.CreateRouterParams{Name: "r", Host: "h", Port: 8728, Username: "a", PasswordEnc: []byte("x"), Enabled: 1})
	plan, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "p", Type: "Hotspot", Billing: "prepaid", Validity: 1, ValidityUnit: "Days",
		BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}, Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := secret.Seal(key, []byte("pw"))
	c, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "alice", PasswordHash: "h", Fullname: "A", ServiceType: "Hotspot", SecretEnc: pw, AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: plan.ID, RouterID: rt.ID, Type: "Hotspot",
		StartedAt: e.now.Unix() - 100, ExpiresAt: e.now.Unix() + 3600}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) auth(t *testing.T, p *radius.Packet) *radius.Packet {
	t.Helper()
	rc := &rec{}
	e.s.HandleAuth(rc, &radius.Request{Packet: p, RemoteAddr: &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5000}})
	return rc.p
}

func pap(user, pass string) *radius.Packet {
	p := radius.New(radius.CodeAccessRequest, nasSecret)
	rfc2865.UserName_SetString(p, user)
	rfc2865.UserPassword_SetString(p, pass)
	return p
}

func TestPAP(t *testing.T) {
	e := setup(t)
	r := e.auth(t, pap("alice", "pw"))
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("code %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
	}
	if got := rfc2865.SessionTimeout_Get(r); got != 3600 {
		t.Fatalf("session-timeout %d", got)
	}
	if got, _ := mikrotik.MikrotikRateLimit_LookupString(r); got != "512K/5M 10M/10M" {
		t.Fatalf("rate %q", got)
	}
	if r := e.auth(t, pap("alice", "bad")); r.Code != radius.CodeAccessReject {
		t.Fatalf("bad password: %v", r.Code)
	}
	if r := e.auth(t, pap("nobody", "pw")); r.Code != radius.CodeAccessReject {
		t.Fatalf("unknown user: %v", r.Code)
	}
}

func TestCHAP(t *testing.T) {
	e := setup(t)
	p := radius.New(radius.CodeAccessRequest, nasSecret)
	rfc2865.UserName_SetString(p, "alice")
	ch := []byte("0123456789abcdef")
	rfc2865.CHAPChallenge_Set(p, ch)
	sum := md5.Sum(append(append([]byte{7}, "pw"...), ch...))
	rfc2865.CHAPPassword_Set(p, append([]byte{7}, sum[:]...))
	if r := e.auth(t, p); r.Code != radius.CodeAccessAccept {
		t.Fatalf("code %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
	}
	sum[0] ^= 1
	rfc2865.CHAPPassword_Set(p, append([]byte{7}, sum[:]...))
	if r := e.auth(t, p); r.Code != radius.CodeAccessReject {
		t.Fatalf("bad chap: %v", r.Code)
	}
}

func TestMSCHAPv2(t *testing.T) {
	e := setup(t)
	p := radius.New(radius.CodeAccessRequest, nasSecret)
	rfc2865.UserName_SetString(p, "alice")
	ach, pch := []byte("0123456789abcdef"), []byte("fedcba9876543210")
	nt, err := rfc2759.GenerateNTResponse(ach, pch, []byte("alice"), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	resp := append(append(append([]byte{1, 0}, pch...), make([]byte, 8)...), nt...)
	microsoft.MSCHAPChallenge_Set(p, ach)
	microsoft.MSCHAP2Response_Set(p, resp)
	r := e.auth(t, p)
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("code %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
	}
	if len(microsoft.MSCHAP2Success_Get(r)) == 0 {
		t.Fatal("no MS-CHAP2-Success")
	}
}

func TestUnknownNASDropped(t *testing.T) {
	e := setup(t)
	if _, err := e.s.RADIUSSecret(context.Background(), &net.UDPAddr{IP: net.ParseIP("192.168.9.9")}); err == nil {
		t.Fatal("unknown NAS must have no secret")
	}
	got, err := e.s.RADIUSSecret(context.Background(), &net.UDPAddr{IP: net.ParseIP("10.0.0.77")})
	if err != nil || string(got) != string(nasSecret) {
		t.Fatalf("cidr match: %q %v", got, err)
	}
}

func TestExpired(t *testing.T) {
	e := setup(t)
	e.now = e.now.Add(2 * time.Hour)
	if r := e.auth(t, pap("alice", "pw")); r.Code != radius.CodeAccessReject {
		t.Fatalf("expired should reject like PHP, got %v", r.Code)
	}
	e.trusted = false
	r := e.auth(t, pap("alice", "pw"))
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("untrusted clock must not reject, got %v", r.Code)
	}
	if _, err := rfc2865.SessionTimeout_Lookup(r); err == nil {
		t.Fatal("no session-timeout for expired plan")
	}
}

func TestAccounting(t *testing.T) {
	e := setup(t)
	acct := func(typ rfc2866.AcctStatusType, in, out uint32) {
		p := radius.New(radius.CodeAccountingRequest, nasSecret)
		rfc2866.AcctStatusType_Set(p, typ)
		rfc2866.AcctSessionID_SetString(p, "sess1")
		rfc2865.UserName_SetString(p, "alice")
		rfc2866.AcctInputOctets_Set(p, rfc2866.AcctInputOctets(in))
		rfc2866.AcctOutputOctets_Set(p, rfc2866.AcctOutputOctets(out))
		rc := &rec{}
		e.s.HandleAcct(rc, &radius.Request{Packet: p, RemoteAddr: &net.UDPAddr{IP: net.ParseIP("10.0.0.1")}})
		if rc.p == nil || rc.p.Code != radius.CodeAccountingResponse {
			t.Fatal("no accounting response")
		}
		e.now = e.now.Add(time.Minute)
	}
	acct(rfc2866.AcctStatusType_Value_Start, 0, 0)
	n, _ := e.q.CountOpenRadiusSessions(context.Background(), "alice")
	if n != 1 {
		t.Fatalf("open %d", n)
	}
	acct(rfc2866.AcctStatusType_Value_InterimUpdate, 100, 200)
	acct(rfc2866.AcctStatusType_Value_Stop, 300, 400)
	n, _ = e.q.CountOpenRadiusSessions(context.Background(), "alice")
	if n != 0 {
		t.Fatalf("still open: %d", n)
	}
	used, _ := e.q.SumRadiusUsage(context.Background(), db.SumRadiusUsageParams{Username: "alice"})
	if used != 700 {
		t.Fatalf("usage %d, want 700 from one row", used)
	}
}
