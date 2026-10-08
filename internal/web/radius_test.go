package web

import (
	"database/sql"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"layeh.com/radius"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

func TestRadiusSessionsPage(t *testing.T) {
	s, h, q, c := crudApp(t)
	ctx, now := t.Context(), time.Now().Unix()
	sec := []byte("nas-secret")

	// a NAS that ACKs every Disconnect-Request
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if p, err := radius.Parse(buf[:n], sec); err == nil {
				b, _ := p.Response(radius.CodeDisconnectACK).Encode()
				pc.WriteTo(b, from)
			}
		}
	}()
	s.CoAPort = strconv.Itoa(pc.LocalAddr().(*net.UDPAddr).Port)
	enc, _ := secret.Seal(s.SecretKey, sec)
	q.CreateNAS(ctx, db.CreateNASParams{Name: "lo", Ip: "127.0.0.1", SecretEnc: enc})

	add := func(user, sid string, updated int64, stopped sql.NullInt64) {
		if err := q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: sid, Username: user, NasIp: "127.0.0.1",
			FramedIp: "10.9.0.5", Mac: "AA:BB", StartedAt: now - 3700, UpdatedAt: updated, StoppedAt: stopped, InputOctets: 1536, OutputOctets: 3 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	add("open-user", "s1", now, sql.NullInt64{})
	add("stale-user", "s2", now-5000, sql.NullInt64{})
	add("closed-user", "s3", now, sql.NullInt64{Int64: now, Valid: true})

	w := do(h, "GET", "/admin/radius/sessions", nil, c)
	wantCode(t, w, 200, "sessions")
	body := w.Body.String()
	for _, want := range []string{"open-user", "stale-user", "10.9.0.5", "1.50 KB", "3.00 MB", "1h 01m", "badge-warn"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "closed-user") {
		t.Error("closed session listed")
	}
	if b := do(h, "GET", "/admin/radius/sessions?q=stale", nil, c).Body.String(); strings.Contains(b, "open-user") || !strings.Contains(b, "stale-user") {
		t.Error("search filter wrong")
	}

	ss, _ := q.SearchOpenRadiusSessions(ctx, db.SearchOpenRadiusSessionsParams{PageLimit: 10})
	wantCode(t, do(h, "POST", "/admin/radius/sessions/"+itoa(ss[0].ID)+"/disconnect", nil, c), 303, "disconnect")
	if b := do(h, "GET", "/admin/radius/sessions", nil, c).Body.String(); !strings.Contains(b, "Pengguna diputus") {
		t.Error("no success flash")
	}
	// a NAS that is not configured shows an error flash
	q.DeleteNAS(ctx, 1)
	do(h, "POST", "/admin/radius/sessions/"+itoa(ss[0].ID)+"/disconnect", nil, c)
	if b := do(h, "GET", "/admin/radius/sessions", nil, c).Body.String(); !strings.Contains(b, "Gagal memutus") {
		t.Error("no error flash")
	}
}

func TestRadiusSessionsReportForbidden(t *testing.T) {
	_, h, _, _ := crudApp(t)
	r := login(t, h, "rita")
	wantCode(t, do(h, "GET", "/admin/radius/sessions", nil, r), 403, "GET")
	wantCode(t, do(h, "POST", "/admin/radius/sessions/1/disconnect", nil, r), 403, "POST")
}

func TestCustomerRadiusUsage(t *testing.T) {
	_, h, q, c := crudApp(t)
	ctx, now := t.Context(), time.Now().Unix()
	rt, _ := q.CreateRouter(ctx, db.CreateRouterParams{Name: "r", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	plan, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "p", Type: "Hotspot", Billing: "prepaid", Validity: 1, ValidityUnit: "Days", Device: "Radius", BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true},
		Enabled: 1, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	cu, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "bob", PasswordHash: "h", Fullname: "B", ServiceType: "Hotspot", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: cu.ID, PlanID: plan.ID, RouterID: rt.ID, Type: "Hotspot",
		StartedAt: now - 1000, ExpiresAt: now + 1000}); err != nil {
		t.Fatal(err)
	}
	sess := func(sid string, start, in, out int64) {
		q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: sid, Username: "bob", NasIp: "10.0.0.1", StartedAt: start, UpdatedAt: start, InputOctets: in, OutputOctets: out})
	}
	sess("a", now-500, 1<<20, 2<<20)  // 3 MB
	sess("b", now-100, 1<<20, 1<<20)  // 2 MB
	sess("old", now-9000, 100<<20, 0) // before the subscription: not counted

	body := do(h, "GET", "/admin/customers/"+itoa(cu.ID), nil, c).Body.String()
	if !strings.Contains(body, "Pemakaian RADIUS") || !strings.Contains(body, "5.00 MB") {
		t.Fatalf("usage card missing or wrong total:\n%s", body)
	}
}
