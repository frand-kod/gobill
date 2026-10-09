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
