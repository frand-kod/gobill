package chat

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
	"github.com/frand-kod/gobill/internal/service/payorder"
)

type cenv struct {
	s      *Service
	q      *db.Queries
	conn   *sql.DB
	budi   Caller
	ani    Caller
	plan   db.Plan
	sent   map[string]string
	now    time.Time
	closed bool
}

func csetup(t *testing.T) *cenv {
	t.Helper()
	recent.report, recent.pay = map[int64]time.Time{}, map[int64][]time.Time{}
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q, ctx := db.New(conn), context.Background()
	e := &cenv{q: q, conn: conn, sent: map[string]string{}, now: time.Now()}
	for _, a := range []db.CreateAdminParams{
		{Username: "boss", Role: "SuperAdmin", PasswordHash: "x", Phone: "0811111111"},
		{Username: "adm", Role: "Admin", PasswordHash: "x", Phone: "0844444444"},
		{Username: "rita", Role: "Report", PasswordHash: "x", Phone: "0822222222"},
	} {
		if _, err := q.CreateAdmin(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{"budi", "ani"} {
		c, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: u, Fullname: strings.ToUpper(u), Phone: "62" + u, ServiceType: "PPPoE", Status: "Active", PasswordHash: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if u == "budi" {
			e.budi = Caller{Kind: KindCustomer, CustomerID: c.ID}
		} else {
			e.ani = Caller{Kind: KindCustomer, CustomerID: c.ID}
		}
	}
	e.plan = e.mkPlan(t, "Home", "Radius", 0, 0)
	e.s = &Service{Q: q, Now: func() time.Time { return e.now }, PortalURL: "https://x/login",
		Notify: func(_ context.Context, phone, text string) error { e.sent[phone] = text; return nil }}
	return e
}

func (e *cenv) mkPlan(t *testing.T, name, device string, dataMB, router int64) db.Plan {
	t.Helper()
	bw, err := e.q.CreateBandwidth(context.Background(), db.CreateBandwidthParams{Name: name, RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	p := db.CreatePlanParams{Name: name, Type: "PPPoE", Billing: "prepaid", Price: 100000, Validity: 30, ValidityUnit: "Days",
		Enabled: 1, Device: device, Audience: "public", BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}}
	if router > 0 {
		p.RouterID = sql.NullInt64{Int64: router, Valid: true}
	}
	if dataMB > 0 {
		p.Limited, p.LimitType = 1, sql.NullString{String: "Data_Limit", Valid: true}
		p.DataLimit, p.DataUnit = sql.NullInt64{Int64: dataMB, Valid: true}, sql.NullString{String: "MB", Valid: true}
	}
	pl, err := e.q.CreatePlan(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

func (e *cenv) sub(t *testing.T, c Caller, p db.Plan, exp time.Time) db.Subscription {
	t.Helper()
	s, err := e.q.CreateSubscription(context.Background(), db.CreateSubscriptionParams{CustomerID: c.CustomerID, PlanID: p.ID, Type: p.Type,
		StartedAt: e.now.Add(-24 * time.Hour).Unix(), ExpiresAt: exp.Unix(), Method: "Admin"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *cenv) session(t *testing.T, user string, age int64) {
	t.Helper()
	if err := e.q.UpsertRadiusSession(context.Background(), db.UpsertRadiusSessionParams{SessionID: "s-" + user, Username: user, NasIp: "10.0.0.1",
		StartedAt: e.now.Unix() - 3600, UpdatedAt: e.now.Unix() - age}); err != nil {
		t.Fatal(err)
	}
}

func TestGangguanCauses(t *testing.T) {
	ctx := context.Background()
	soon := func(e *cenv) time.Time { return e.now.Add(10 * 24 * time.Hour) }

	t.Run("isolated", func(t *testing.T) {
		e := csetup(t)
		e.sub(t, e.budi, e.plan, soon(e))
		e.q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: e.budi.CustomerID, StartedAt: e.now.Unix() - 10, EndsAt: e.now.Unix() + 3600})
		if r, _ := e.s.Reply(ctx, e.budi, "gangguan"); !strings.Contains(r, "isolated") || !strings.Contains(r, "https://x/login") {
			t.Fatalf("%q", r)
		}
	})
	t.Run("expired", func(t *testing.T) {
		e := csetup(t)
		e.sub(t, e.budi, e.plan, e.now.Add(-time.Hour))
		if r, _ := e.s.Reply(ctx, e.budi, "mati"); !strings.Contains(r, "ended") {
			t.Fatalf("%q", r)
		}
	})
	t.Run("quota", func(t *testing.T) {
		e := csetup(t)
		p := e.mkPlan(t, "Cap", "Radius", 100, 0)
		s := e.sub(t, e.budi, p, soon(e))
		if _, err := e.conn.Exec("UPDATE subscriptions SET used_in_octets = ? WHERE id = ?", 200<<20, s.ID); err != nil {
			t.Fatal(err)
		}
		e.session(t, "budi", 10)
		if r, _ := e.s.Reply(ctx, e.budi, "lapor"); !strings.Contains(r, "quota") {
			t.Fatalf("%q", r)
		}
	})
	t.Run("router offline", func(t *testing.T) {
		e := csetup(t)
		rt, err := e.q.CreateRouter(ctx, db.CreateRouterParams{Name: "R1", Host: "1.1.1.1", Port: 8728, Enabled: 1, PasswordEnc: []byte("x")})
		if err != nil {
			t.Fatal(err)
		}
		e.q.SetRouterStatus(ctx, db.SetRouterStatusParams{Online: sql.NullInt64{Int64: 0, Valid: true}, ID: rt.ID})
		p := e.mkPlan(t, "Mt", "MikrotikPppoe", 0, rt.ID)
		e.sub(t, e.budi, p, soon(e))
		if r, _ := e.s.Reply(ctx, e.budi, "gangguan"); !strings.Contains(r, "network") || len(e.sent) != 0 {
			t.Fatalf("%q sent=%v", r, e.sent)
		}
	})
	t.Run("router not checked yet is no outage", func(t *testing.T) {
		e := csetup(t)
		rt, err := e.q.CreateRouter(ctx, db.CreateRouterParams{Name: "R1", Host: "1.1.1.1", Port: 8728, Enabled: 1, PasswordEnc: []byte("x")})
		if err != nil {
			t.Fatal(err)
		}
		p := e.mkPlan(t, "Mt", "MikrotikPppoe", 0, rt.ID)
		e.sub(t, e.budi, p, soon(e))
		if r, _ := e.s.Reply(ctx, e.budi, "gangguan"); strings.Contains(r, "network") {
			t.Fatalf("unchecked router reported as outage: %q", r)
		}
	})
	t.Run("device offline", func(t *testing.T) {
		e := csetup(t)
		e.sub(t, e.budi, e.plan, soon(e))
		e.session(t, "budi", 100000) // silent for a day
		if r, _ := e.s.Reply(ctx, e.budi, "offline"); !strings.Contains(r, "modem") || len(e.sent) != 0 {
			t.Fatalf("%q sent=%v", r, e.sent)
		}
	})
	t.Run("account blocked", func(t *testing.T) {
		e := csetup(t)
		e.conn.Exec("UPDATE customers SET status = 'Suspended' WHERE id = ?", e.budi.CustomerID)
		if r, _ := e.s.Reply(ctx, e.budi, "gangguan"); !strings.Contains(r, "Suspended") {
			t.Fatalf("%q", r)
		}
	})
}

// No cause: the report goes to SuperAdmin and Admin with a phone, once per 30 minutes.
func TestGangguanForwardsOnce(t *testing.T) {
	ctx := context.Background()
	e := csetup(t)
	e.sub(t, e.budi, e.plan, e.now.Add(10*24*time.Hour))
	e.session(t, "budi", 10)
	e.sub(t, e.ani, e.plan, e.now.Add(10*24*time.Hour))
	e.session(t, "ani", 10)
	r, _ := e.s.Reply(ctx, e.budi, "gangguan")
	if !strings.Contains(r, "forwarded") || len(e.sent) != 2 || e.sent["0811111111"] == "" || e.sent["0844444444"] == "" || e.sent["0822222222"] != "" {
		t.Fatalf("%q sent=%v", r, e.sent)
	}
	if !strings.Contains(e.sent["0811111111"], "budi") {
		t.Fatalf("admin text: %q", e.sent["0811111111"])
	}
	clear(e.sent)
	if r, _ := e.s.Reply(ctx, e.budi, "gangguan"); !strings.Contains(r, "already") || len(e.sent) != 0 {
		t.Fatalf("second: %q sent=%v", r, e.sent)
	}
	if r, _ := e.s.Reply(ctx, e.ani, "gangguan"); len(e.sent) != 2 { // another customer is not blocked
		t.Fatalf("ani: %q sent=%v", r, e.sent)
	}
	clear(e.sent)
	e.now = e.now.Add(31 * time.Minute)
	e.session(t, "budi", 10)
	e.s.Reply(ctx, e.budi, "gangguan")
	if len(e.sent) != 2 {
		t.Fatalf("after window: %v", e.sent)
	}
}

type fakeGW struct{ calls int }

func (g *fakeGW) Name() string                           { return "fake" }
func (g *fakeGW) ValidateConfig(map[string]string) error { return nil }
func (g *fakeGW) CreateTransaction(_ context.Context, trx payment.Transaction) (payment.Created, error) {
	g.calls++
	return payment.Created{Reference: "R-" + trx.ID, PayURL: "https://pay/" + trx.ID}, nil
}
func (g *fakeGW) Status(context.Context, payment.Transaction) (payment.Status, error) {
	return payment.Status(""), nil
}
func (g *fakeGW) HandleCallback(*http.Request) (string, payment.Status, int64, error) {
	return "", payment.Status(""), 0, nil
}

func (e *cenv) withPay(gw *fakeGW) {
	e.s.Pay = func(ctx context.Context, c db.Customer, p db.Plan) (db.PaymentRequest, error) {
		return payorder.Create(ctx, e.q, gw, payorder.Order{Customer: c, PlanID: p.ID, PlanName: p.Name, Amount: p.Price,
			Channel: "QRIS", ReturnBase: "https://x", CallbackBase: "https://x"})
	}
}

func TestBayar(t *testing.T) {
	ctx := context.Background()
	e := csetup(t)
	e.sub(t, e.budi, e.plan, e.now.Add(24*time.Hour))
	e.q.GetCustomer(ctx, e.budi.CustomerID)

	// no gateway: portal fallback
	if r, _ := e.s.Reply(ctx, e.budi, "bayar"); !strings.Contains(r, "https://x/login") {
		t.Fatalf("fallback: %q", r)
	}
	gw := &fakeGW{}
	e.withPay(gw)
	r, _ := e.s.Reply(ctx, e.budi, "pay")
	if !strings.Contains(r, "https://pay/NB") || !strings.Contains(r, "Home") || gw.calls != 1 {
		t.Fatalf("new: %q calls=%d", r, gw.calls)
	}
	// pending request is reused
	if r2, _ := e.s.Reply(ctx, e.budi, "bayar"); r2 != r || gw.calls != 1 {
		t.Fatalf("reuse: %q calls=%d", r2, gw.calls)
	}
	// ani has her own flow: nothing of budi's request shows
	if r3, _ := e.s.Reply(ctx, e.ani, "bayar"); strings.Contains(r3, "https://pay/") || !strings.Contains(r3, "paket") {
		t.Fatalf("ani without plan: %q", r3)
	}
	var n int
	e.conn.QueryRow("SELECT COUNT(*) FROM payment_requests WHERE customer_id = ?", e.ani.CustomerID).Scan(&n)
	if n != 0 {
		t.Fatalf("ani got %d requests", n)
	}
}

func TestBayarRateLimit(t *testing.T) {
	ctx := context.Background()
	e := csetup(t)
	e.sub(t, e.budi, e.plan, e.now.Add(24*time.Hour))
	gw := &fakeGW{}
	e.withPay(gw)
	for i := 0; i < 3; i++ {
		e.conn.Exec("UPDATE payment_requests SET status = 'failed'")
		if r, _ := e.s.Reply(ctx, e.budi, "bayar"); !strings.Contains(r, "https://pay/") {
			t.Fatalf("%d: %q", i, r)
		}
	}
	e.conn.Exec("UPDATE payment_requests SET status = 'failed'")
	if r, _ := e.s.Reply(ctx, e.budi, "bayar"); !strings.Contains(r, "Too many") || gw.calls != 3 {
		t.Fatalf("4th: %q calls=%d", r, gw.calls)
	}
	e.now = e.now.Add(61 * time.Minute)
	if r, _ := e.s.Reply(ctx, e.budi, "bayar"); !strings.Contains(r, "https://pay/") {
		t.Fatalf("after window: %q", r)
	}
}
