package billing

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

type fakeDev struct {
	device.Dummy
	calls *[]string
}

func (f fakeDev) AddCustomer(_ context.Context, c device.Customer, p device.Plan) error {
	*f.calls = append(*f.calls, "add:"+p.Name)
	return nil
}
func (f fakeDev) RemoveCustomer(_ context.Context, c device.Customer, p device.Plan) error {
	*f.calls = append(*f.calls, "remove:"+p.Name)
	return nil
}
func (f fakeDev) Disconnect(context.Context, device.Customer, string) error {
	*f.calls = append(*f.calls, "disconnect")
	return nil
}

type env struct {
	s     *Service
	conn  *sql.DB
	q     *db.Queries
	now   time.Time
	calls *[]string
	cust  db.Customer
	day   db.Plan // 1 Day, 10000
	day2  db.Plan // other plan, 20000, same router/type
	topup db.Plan // Balance plan
}

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
	e := &env{conn: conn, q: q, now: time.Date(2025, 1, 10, 12, 0, 0, 0, jkt), calls: new([]string)}
	e.s = &Service{DB: conn, Q: q, Loc: jkt, Now: func() time.Time { return e.now },
		DeviceFor: func(db.Plan, db.Router) (device.Device, error) { return fakeDev{calls: e.calls}, nil }}
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	rt, err := q.CreateRouter(ctx, db.CreateRouterParams{Name: "r", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name, typ string, price int64) db.Plan {
		p := db.CreatePlanParams{Name: name, Type: typ, Billing: "prepaid", Price: price, Validity: 1, ValidityUnit: "Days", Enabled: 1}
		if typ != "Balance" {
			p.BandwidthID, p.RouterID = sql.NullInt64{Int64: bw.ID, Valid: true}, sql.NullInt64{Int64: rt.ID, Valid: true}
		}
		pl, err := q.CreatePlan(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return pl
	}
	e.day, e.day2, e.topup = mk("day", "PPPoE", 10000), mk("day2", "PPPoE", 20000), mk("topup", "Balance", 50000)
	e.cust, err = q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u1", PasswordHash: "h", Fullname: "U", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) subs(t *testing.T) []db.Subscription {
	t.Helper()
	s, err := e.q.ListSubscriptionsByCustomer(context.Background(), db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) sub(t *testing.T) db.Subscription {
	t.Helper()
	for _, s := range e.subs(t) {
		if s.Status == "active" {
			return s
		}
	}
	t.Fatal("no active subscription")
	return db.Subscription{}
}

func (e *env) balance() int64 {
	c, _ := e.q.GetCustomer(context.Background(), e.cust.ID)
	return c.Balance
}

func TestRechargeNewExtendChange(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	s1 := e.sub(t)
	if want := e.now.AddDate(0, 0, 1).Unix(); s1.ExpiresAt != want {
		t.Fatalf("new expiry %d want %d", s1.ExpiresAt, want)
	}
	trx, err := e.q.GetTransactionByInvoice(ctx, "INV-2501-000001")
	if err != nil || trx.Price != 10000 || trx.RouterName != "r" || trx.Method != "Admin - Cash" {
		t.Fatalf("trx %+v %v", trx, err)
	}
	// extend: adds to the old expiry, same row, no disconnect
	e.now = e.now.Add(time.Hour)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	s2 := e.sub(t)
	if s2.ID != s1.ID || s2.ExpiresAt != e.now.Add(-time.Hour).AddDate(0, 0, 2).Unix() {
		t.Fatalf("extend: %+v", s2)
	}
	if len(*e.calls) != 2 {
		t.Fatalf("calls %v", *e.calls)
	}
	// plan change: replaces from now, disconnects (PPPoE)
	*e.calls = nil
	if err := e.s.Recharge(ctx, e.cust.ID, e.day2.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	s3 := e.sub(t)
	if s3.ID != s1.ID || s3.PlanID != e.day2.ID || s3.ExpiresAt != e.now.AddDate(0, 0, 1).Unix() {
		t.Fatalf("change: %+v", s3)
	}
	if len(*e.calls) != 2 || (*e.calls)[1] != "disconnect" {
		t.Fatalf("calls %v", *e.calls)
	}
}

func TestBalancePlan(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.topup.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if e.balance() != 50000 || len(*e.calls) != 0 || len(e.subs(t)) != 0 {
		t.Fatalf("balance %d calls %v", e.balance(), *e.calls)
	}
}

func TestVoucherOnce(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: "ABC", PlanID: e.day.ID})
	if err := e.s.RedeemVoucher(ctx, "ABC", e.cust.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RedeemVoucher(ctx, "ABC", e.cust.ID); !errors.Is(err, ErrVoucherInvalid) {
		t.Fatalf("second redeem: %v", err)
	}
	if s := e.sub(t); s.Method != "Voucher - ABC" {
		t.Fatalf("method %q", s.Method)
	}
	if err := e.s.RedeemVoucher(ctx, "NOPE", e.cust.ID); !errors.Is(err, ErrVoucherInvalid) {
		t.Fatal(err)
	}
}

func TestRechargeWithBalance(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 5000, ID: e.cust.ID})
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("err %v", err)
	}
	trx, _ := e.q.ListTransactions(ctx, db.ListTransactionsParams{Limit: 10})
	if e.balance() != 5000 || len(e.subs(t)) != 0 || len(trx) != 0 || len(*e.calls) != 0 {
		t.Fatal("side effects after rejected payment")
	}
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 5000, ID: e.cust.ID})
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); err != nil {
		t.Fatal(err)
	}
	if e.balance() != 0 {
		t.Fatalf("balance %d", e.balance())
	}
}

func TestExpireDueIdempotent(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	*e.calls = nil
	e.now = e.now.AddDate(0, 0, 2)
	for i := 0; i < 2; i++ {
		if err := e.s.ExpireDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(*e.calls) != 1 || (*e.calls)[0] != "remove:day" {
		t.Fatalf("calls %v", *e.calls)
	}
	if s := e.subs(t); len(s) != 1 || s[0].Status != "expired" {
		t.Fatalf("subs %+v", s)
	}
}

func TestExpireDueAutoRenew(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 10000, ID: e.cust.ID})
	e.now = e.now.AddDate(0, 0, 2)
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	s := e.sub(t) // a fresh active row
	if s.ExpiresAt != e.now.AddDate(0, 0, 1).Unix() || e.balance() != 0 || s.Method != "Customer - Balance" {
		t.Fatalf("renewal: %+v balance %d", s, e.balance())
	}
	// no balance: expires and stays expired
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	for _, x := range e.subs(t) {
		if x.Status == "active" {
			t.Fatal("renewed without balance")
		}
	}
}

func TestExpiryJobSkipsUntrusted(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 2)
	if err := e.s.ExpiryJob(func() bool { return false })(ctx); err != nil {
		t.Fatal(err)
	}
	if e.sub(t).Status != "active" {
		t.Fatal("expired on untrusted clock")
	}
	if err := e.s.ExpiryJob(func() bool { return true })(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.subs(t)) != 1 || e.subs(t)[0].Status != "expired" {
		t.Fatal("not expired on trusted clock")
	}
}

func TestExpireOneSkipsRenewedSnapshot(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.Add(36 * time.Hour)
	stale := e.sub(t)                                                                 // the job's snapshot
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil { // renewed before expireOne
		t.Fatal(err)
	}
	*e.calls = nil
	if err := e.s.expireOne(ctx, stale, false); err != nil {
		t.Fatal(err)
	}
	if len(*e.calls) != 0 || e.sub(t).Status != "active" {
		t.Fatalf("renewed sub was expired: calls %v", *e.calls)
	}
}
