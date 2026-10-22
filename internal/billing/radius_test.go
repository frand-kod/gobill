package billing

import (
	"context"
	"database/sql"
	"net"
	"strconv"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/vendors/mikrotik"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
)

func TestRadiusPlanRechargeWithoutRouter(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	plan, err := e.q.CreatePlan(ctx, db.CreatePlanParams{Name: "rad-nort", Type: "PPPoE", Billing: "prepaid", Price: 1, Validity: 1,
		ValidityUnit: "Days", Device: "Radius", Enabled: 1, BandwidthID: e.day.BandwidthID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.Recharge(ctx, e.cust.ID, plan.ID, "Cash", 0); err != nil {
		t.Fatal(err)
	}
	if s := e.sub(t); s.RouterID.Valid || s.PlanID != plan.ID {
		t.Fatalf("sub %+v", s)
	}
}

func TestExpireRadiusPlanDisconnects(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	key, sec := make([]byte, 32), []byte("nas-secret")
	e.s.Key = key

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	users := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		p, err := radius.Parse(buf[:n], sec)
		if err != nil || !radius.IsAuthenticRequest(buf[:n], sec) || p.Code != radius.CodeDisconnectRequest {
			return
		}
		users <- rfc2865.UserName_GetString(p)
		b, _ := p.Response(radius.CodeDisconnectACK).Encode()
		pc.WriteTo(b, from)
	}()
	port := strconv.Itoa(pc.LocalAddr().(*net.UDPAddr).Port)
	e.s.DeviceFor = func(p db.Plan, _ db.Router) (device.Device, error) {
		return device.Radius{Q: e.q, Key: key, Port: port}, nil
	}

	enc, _ := secret.Seal(key, sec)
	if _, err := e.q.CreateNAS(ctx, db.CreateNASParams{Name: "lo", Ip: "127.0.0.1", SecretEnc: enc}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "s1", Username: "u1", NasIp: "127.0.0.1",
		StartedAt: e.now.Unix(), UpdatedAt: e.now.Unix()}); err != nil {
		t.Fatal(err)
	}
	plan, err := e.q.CreatePlan(ctx, db.CreatePlanParams{Name: "rad", Type: "PPPoE", Billing: "prepaid", Price: 1, Validity: 1, ValidityUnit: "Days",
		Device: "Radius", Enabled: 1, RouterID: sql.NullInt64{Int64: e.day.RouterID.Int64, Valid: true}, BandwidthID: e.day.BandwidthID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: plan.ID, RouterID: plan.RouterID,
		Type: "PPPoE", StartedAt: e.now.Unix() - 200, ExpiresAt: e.now.Unix() - 100}); err != nil {
		t.Fatal(err)
	}

	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case u := <-users:
		if u != "u1" {
			t.Fatalf("disconnected %q", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Disconnect-Request reached the NAS")
	}
	if s := e.subs(t); len(s) != 1 || s[0].Status != "expired" {
		t.Fatalf("subs %+v", s)
	}
}

// A recharge of an online user sends CoA with the plan's rate; a NAK falls back to Disconnect.
// An admin plan edit sends CoA to the plan's online users.
func TestRadiusPlanChangeCoA(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	key, sec := make([]byte, 32), []byte("nas-secret")
	e.s.Key = key
	e.now = time.Now() // the device computes Session-Timeout against the wall clock

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	got := make(chan *radius.Packet, 4)
	nak := true // the first CoA is refused
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			p, err := radius.Parse(buf[:n], sec)
			if err != nil || !radius.IsAuthenticRequest(buf[:n], sec) {
				continue
			}
			code := radius.CodeDisconnectACK
			if p.Code == radius.CodeCoARequest {
				code = radius.CodeCoAACK
				if nak {
					code, nak = radius.CodeCoANAK, false
				}
			}
			got <- p
			b, _ := p.Response(code).Encode()
			pc.WriteTo(b, from)
		}
	}()
	port := strconv.Itoa(pc.LocalAddr().(*net.UDPAddr).Port)
	e.s.DeviceFor = func(p db.Plan, _ db.Router) (device.Device, error) {
		return device.Radius{Q: e.q, Key: key, Port: port}, nil
	}
	enc, _ := secret.Seal(key, sec)
	if _, err := e.q.CreateNAS(ctx, db.CreateNASParams{Name: "lo", Ip: "127.0.0.1", SecretEnc: enc}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "s1", Username: e.cust.Username, NasIp: "127.0.0.1",
		StartedAt: e.now.Unix(), UpdatedAt: e.now.Unix()}); err != nil {
		t.Fatal(err)
	}
	plan, err := e.q.CreatePlan(ctx, db.CreatePlanParams{Name: "rad", Type: "PPPoE", Billing: "prepaid", Price: 1, Validity: 1, ValidityUnit: "Days",
		Device: "Radius", Enabled: 1, RouterID: e.day.RouterID, BandwidthID: e.day.BandwidthID})
	if err != nil {
		t.Fatal(err)
	}
	next := func() *radius.Packet {
		t.Helper()
		select {
		case p := <-got:
			return p
		case <-time.After(5 * time.Second):
			t.Fatal("no request reached the NAS")
			return nil
		}
	}
	coa := func(p *radius.Packet) {
		t.Helper()
		if p.Code != radius.CodeCoARequest || mikrotik.MikrotikRateLimit_GetString(p) != "1M/1M" || rfc2865.SessionTimeout_Get(p) == 0 {
			t.Fatalf("CoA %v rate %q timeout %d", p.Code, mikrotik.MikrotikRateLimit_GetString(p), rfc2865.SessionTimeout_Get(p))
		}
	}

	if err := e.s.Recharge(ctx, e.cust.ID, plan.ID, "Cash", 0); err != nil {
		t.Fatal(err)
	}
	coa(next())
	if p := next(); p.Code != radius.CodeDisconnectRequest {
		t.Fatalf("after NAK got %v, want Disconnect-Request", p.Code)
	}
	if err := e.s.SyncPlan(ctx, "update", plan, plan.Name); err != nil {
		t.Fatal(err)
	}
	coa(next())
}
