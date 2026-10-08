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
	"layeh.com/radius/rfc2869"
	"layeh.com/radius/vendors/microsoft"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

var nasSecret = []byte("nas-secret")

type rec struct{ p *radius.Packet }

func (r *rec) Write(p *radius.Packet) error { r.p = p; return nil }

type env struct {
	conn    *sql.DB
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
	e := &env{q: q, conn: conn, now: time.Unix(1_000_000, 0), trusted: true}
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
	if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: plan.ID, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}, Type: "Hotspot",
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
	p.Identifier++ // same id and authenticator would be a retransmit
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
	n, _ := e.openSessions()
	if n != 1 {
		t.Fatalf("open %d", n)
	}
	acct(rfc2866.AcctStatusType_Value_InterimUpdate, 100, 200)
	acct(rfc2866.AcctStatusType_Value_Stop, 300, 400)
	n, _ = e.openSessions()
	if n != 0 {
		t.Fatalf("still open: %d", n)
	}
	used, _ := e.q.SumRadiusUsage(context.Background(), db.SumRadiusUsageParams{Username: "alice"})
	if used != 700 {
		t.Fatalf("usage %d, want 700 from one row", used)
	}
}

func (e *env) openSessions() (int64, error) {
	var n int64
	err := e.conn.QueryRow("SELECT COUNT(*) FROM radius_sessions WHERE stopped_at IS NULL").Scan(&n)
	return n, err
}

func (e *env) sendAcct(t *testing.T, typ rfc2866.AcctStatusType, sid, ip, mac string) {
	t.Helper()
	p := radius.New(radius.CodeAccountingRequest, nasSecret)
	rfc2866.AcctStatusType_Set(p, typ)
	rfc2866.AcctSessionID_SetString(p, sid)
	rfc2865.UserName_SetString(p, "alice")
	if ip != "" {
		rfc2865.FramedIPAddress_Set(p, net.ParseIP(ip))
	}
	rfc2865.CallingStationID_SetString(p, mac)
	e.s.HandleAcct(&rec{}, &radius.Request{Packet: p, RemoteAddr: &net.UDPAddr{IP: net.ParseIP("10.0.0.1")}})
}

func (e *env) authFrom(t *testing.T, ip, mac string) *radius.Packet {
	p := pap("alice", "pw")
	if ip != "" {
		rfc2865.FramedIPAddress_Set(p, net.ParseIP(ip))
	}
	rfc2865.CallingStationID_SetString(p, mac)
	return e.auth(t, p)
}

func (e *env) setShared(t *testing.T, n int) {
	if _, err := e.conn.Exec("UPDATE plans SET shared_users = ?", n); err != nil {
		t.Fatal(err)
	}
}

func TestSharedUsers(t *testing.T) {
	e := setup(t)
	e.setShared(t, 1)
	e.sendAcct(t, rfc2866.AcctStatusType_Value_Start, "s1", "172.16.0.5", "AA:AA")
	if r := e.authFrom(t, "172.16.0.5", "AA:AA"); r.Code != radius.CodeAccessAccept {
		t.Fatal("reconnect from same IP must be accepted")
	}
	if r := e.authFrom(t, "", "AA:AA"); r.Code != radius.CodeAccessAccept {
		t.Fatal("reconnect with same MAC must be accepted")
	}
	if r := e.authFrom(t, "172.16.0.9", "BB:BB"); r.Code != radius.CodeAccessReject {
		t.Fatal("different device at the limit must be rejected")
	}
	e.now = e.now.Add(staleAfter*time.Second + time.Second)
	if r := e.authFrom(t, "172.16.0.9", "BB:BB"); r.Code != radius.CodeAccessAccept {
		t.Fatal("stale session must be ignored")
	}
	if n, _ := e.openSessions(); n != 1 {
		t.Fatalf("stale row must not be deleted/closed, open=%d", n)
	}
}

func TestAccountingOnClosesSessions(t *testing.T) {
	e := setup(t)
	e.sendAcct(t, rfc2866.AcctStatusType_Value_Start, "s1", "172.16.0.5", "AA")
	e.sendAcct(t, rfc2866.AcctStatusType_Value_Start, "s2", "172.16.0.6", "BB")
	e.sendAcct(t, rfc2866.AcctStatusType_Value_AccountingOn, "", "", "")
	if n, _ := e.openSessions(); n != 0 {
		t.Fatalf("open %d", n)
	}
}

func TestDataLimit(t *testing.T) {
	e := setup(t)
	if _, err := e.conn.Exec("UPDATE plans SET limited=1, limit_type='Data_Limit', data_limit=1, data_unit='MB'"); err != nil {
		t.Fatal(err)
	}
	r := e.auth(t, pap("alice", "pw"))
	if r.Code != radius.CodeAccessAccept {
		t.Fatal("under limit must accept")
	}
	if got, _ := mikrotik.MikrotikTotalLimit_Lookup(r); got != 1048576 {
		t.Fatalf("total limit %d", got)
	}
	e.sendAcct(t, rfc2866.AcctStatusType_Value_Stop, "s1", "", "")
	if _, err := e.conn.Exec("UPDATE radius_sessions SET input_octets = 1048576"); err != nil {
		t.Fatal(err)
	}
	if r := e.auth(t, pap("alice", "pw")); r.Code != radius.CodeAccessReject {
		t.Fatal("over limit must reject")
	}
}

func TestPPPoEAttrs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rt, _ := e.q.ListRouters(ctx, db.ListRoutersParams{Limit: 1})
	pool, err := e.q.CreatePool(ctx, db.CreatePoolParams{Name: "pppoe-pool", RangeIp: "10.9.0.2-10.9.0.9", RouterID: rt[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.conn.Exec("UPDATE plans SET type='PPPoE', pool_id=?; UPDATE customers SET pppoe_ip='10.9.0.7'", pool.ID); err != nil {
		t.Fatal(err)
	}
	r := e.auth(t, pap("alice", "pw"))
	if r.Code != radius.CodeAccessAccept {
		t.Fatal("reject")
	}
	if got := rfc2869.FramedPool_GetString(r); got != "pppoe-pool" {
		t.Fatalf("pool %q", got)
	}
	if got := rfc2865.FramedIPAddress_Get(r).String(); got != "10.9.0.7" {
		t.Fatalf("ip %s", got)
	}
}

func TestEmptySecretRejected(t *testing.T) {
	e := setup(t)
	empty, _ := secret.Seal(e.s.Key, []byte(""))
	for name, enc := range map[string][]byte{"none": nil, "sealed-empty": empty} {
		if _, err := e.q.CreateCustomer(context.Background(), db.CreateCustomerParams{Username: "e-" + name, PasswordHash: "h", Fullname: "E", ServiceType: "Hotspot", SecretEnc: enc, Status: "Active"}); err != nil {
			t.Fatal(err)
		}
		if r := e.auth(t, pap("e-"+name, "")); r.Code != radius.CodeAccessReject {
			t.Fatalf("%s: empty password accepted", name)
		}
	}
}
