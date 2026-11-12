package chat

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/clock"
	"github.com/frand-kod/gobill/internal/db"
)

var testNow = time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

func adminSetup(t *testing.T) (*Service, *db.Queries, db.Plan) {
	t.Helper()
	s, q := setup(t)
	s.Now = clock.Func(func() time.Time { return testNow })
	bw, err := q.CreateBandwidth(context.Background(), db.CreateBandwidthParams{Name: "bw", RateDown: 10, RateDownUnit: "Mbps", RateUp: 10, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := q.CreatePlan(context.Background(), db.CreatePlanParams{Name: "Home", Type: "PPPoE", Billing: "prepaid", BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true},
		Validity: 30, ValidityUnit: "Days", Enabled: 1, Device: "Radius"})
	if err != nil {
		t.Fatal(err)
	}
	return s, q, p
}

func subscribe(t *testing.T, q *db.Queries, username string, plan db.Plan, expires time.Time) {
	t.Helper()
	cu, err := q.GetCustomerByUsername(context.Background(), username)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateSubscription(context.Background(), db.CreateSubscriptionParams{CustomerID: cu.ID, PlanID: plan.ID,
		Type: "PPPoE", StartedAt: testNow.Unix(), ExpiresAt: expires.Unix(), Method: "test"}); err != nil {
		t.Fatal(err)
	}
}

func TestRoleDeniedMessage(t *testing.T) {
	s, _, _ := adminSetup(t)
	ctx := context.Background()
	sales, _ := s.Identify(ctx, "0833333333")
	if r, tool := s.Reply(ctx, sales, "cari budi"); tool != "" || r != "You are not allowed to use this command." {
		t.Fatalf("denied: %q %q", r, tool)
	}
	if r, _ := s.Reply(ctx, sales, "bogus"); !strings.Contains(r, "not known") {
		t.Fatalf("unknown: %q", r)
	}
	budi, _ := s.Identify(ctx, "628123456789")
	if r, _ := s.Reply(ctx, budi, "router"); !strings.Contains(r, "not known") {
		t.Fatalf("customer must not learn admin commands: %q", r)
	}
}

func TestCariWithoutPhone(t *testing.T) {
	s, q, _ := adminSetup(t)
	ctx := context.Background()
	if _, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "nophone", Fullname: "No Phone", ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"}); err != nil {
		t.Fatal(err)
	}
	boss, _ := s.Identify(ctx, "0811111111")
	r, _ := s.Reply(ctx, boss, "cari nophone")
	if r != "nophone - No Phone - Active" {
		t.Fatalf("cari: %q", r)
	}
}

func TestInfo(t *testing.T) {
	s, q, p := adminSetup(t)
	ctx := context.Background()
	subscribe(t, q, "budi", p, testNow.Add(5*24*time.Hour))
	if err := q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "s1", Username: "budi", NasIp: "10.0.0.1",
		FramedIp: "172.16.0.9", StartedAt: testNow.Unix(), UpdatedAt: testNow.Unix()}); err != nil {
		t.Fatal(err)
	}
	boss, _ := s.Identify(ctx, "0811111111")
	r, tool := s.Reply(ctx, boss, "info budi")
	if tool != "info" {
		t.Fatalf("tool %q", tool)
	}
	for _, want := range []string{"Budi Santoso", "628123456789", "Home until 2026-05-15", "5 days left", "Isolated: no", "Online, IP 172.16.0.9"} {
		if !strings.Contains(r, want) {
			t.Fatalf("missing %q in %q", want, r)
		}
	}
	if r, _ := s.Reply(ctx, boss, "info ani"); !strings.Contains(r, "Offline") {
		t.Fatalf("ani: %q", r)
	}
	if r, _ := s.Reply(ctx, boss, "info nobody"); r != "No customer found." {
		t.Fatalf("not found: %q", r)
	}
	// a customer's "info" is still the customer tool (alias of cek), never the admin one
	budi, _ := s.Identify(ctx, "628123456789")
	if r, tool := s.Reply(ctx, budi, "info ani"); tool != "cek" || strings.Contains(r, "Ani") {
		t.Fatalf("customer info: %q %q", r, tool)
	}
	for _, cmd := range []string{"jatuhtempo", "router"} {
		if _, tool := s.Reply(ctx, budi, cmd); tool != "" {
			t.Fatalf("customer ran %s", cmd)
		}
	}
}

func TestJatuhTempoWindow(t *testing.T) {
	s, q, p := adminSetup(t)
	ctx := context.Background()
	subscribe(t, q, "budi", p, testNow.Add(2*24*time.Hour))
	subscribe(t, q, "ani", p, testNow.Add(10*24*time.Hour))
	boss, _ := s.Identify(ctx, "0811111111")
	r, _ := s.Reply(ctx, boss, "jatuhtempo")
	if !strings.Contains(r, "budi") || !strings.Contains(r, "2026-05-12") || strings.Contains(r, "ani") {
		t.Fatalf("jatuhtempo: %q", r)
	}
}

func TestRouterOffline(t *testing.T) {
	s, q, _ := adminSetup(t)
	ctx := context.Background()
	boss, _ := s.Identify(ctx, "0811111111")
	if r, _ := s.Reply(ctx, boss, "router"); !strings.Contains(r, "online") {
		t.Fatalf("empty: %q", r)
	}
	for _, name := range []string{"rt-up", "rt-down"} {
		r, err := q.CreateRouter(ctx, db.CreateRouterParams{Name: name, Host: name, Port: 8728, Enabled: 1, PasswordEnc: []byte("x")})
		if err != nil {
			t.Fatal(err)
		}
		on := int64(0)
		if name == "rt-up" {
			on = 1
		}
		if err := q.SetRouterStatus(ctx, db.SetRouterStatusParams{ID: r.ID, Online: sql.NullInt64{Int64: on, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []struct{ name, ip string }{{"nas-old", "10.0.0.1"}, {"nas-new", "10.0.0.2"}} {
		if _, err := q.CreateNAS(ctx, db.CreateNASParams{Name: n.name, Ip: n.ip, SecretEnc: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		seen := testNow.Add(-2 * time.Hour)
		if n.name == "nas-new" {
			seen = testNow.Add(-time.Minute)
		}
		if err := q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: n.name, Username: "x", NasIp: n.ip,
			StartedAt: seen.Unix(), UpdatedAt: seen.Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := s.Reply(ctx, boss, "router")
	if !strings.Contains(r, "Router rt-down") || !strings.Contains(r, "NAS nas-old") ||
		strings.Contains(r, "rt-up") || strings.Contains(r, "nas-new") {
		t.Fatalf("router: %q", r)
	}
}
