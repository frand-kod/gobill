package billing

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

// F1: renewing an active-but-past-expiry row must not land in the past.
func TestAuditLifecycle1_ExtendFromPastExpiry(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.AddDate(0, 0, 3) // expiry job did not run (clock untrusted / router down)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if got, min := e.sub(t).ExpiresAt, e.now.AddDate(0, 0, 1).Unix(); got < min {
		t.Fatalf("paid 1 day, expires %v (now %v)", time.Unix(got, 0).In(jkt), e.now)
	}
}

// F2 / router #4: auto-renew must not kick the paying customer off the router.
func TestAuditLifecycle2_AutoRenewNoRemove(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 10000, ID: e.cust.ID})
	e.now = e.now.AddDate(0, 0, 2)
	*e.calls = nil
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	if e.sub(t).Status != "active" {
		t.Fatal("not renewed")
	}
	if slices.Contains(*e.calls, "remove:day") {
		t.Fatalf("renewed customer was removed from the router: %v", *e.calls)
	}
}

// F4: an admin extension revives the plan but leaves the isolation running.
func TestAuditLifecycle4_AdminExtendEndsIsolation(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if _, err := e.conn.Exec("UPDATE plans SET router_id = NULL, device = 'Radius'"); err != nil {
		t.Fatal(err)
	}
	e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "isolir_enabled", Value: "yes"})
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	id := e.sub(t).ID
	e.now = e.now.AddDate(0, 0, 2)
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	running := func() bool {
		_, err := e.q.GetRunningIsolation(ctx, db.GetRunningIsolationParams{CustomerID: e.cust.ID, Now: e.now.Unix()})
		return err == nil
	}
	if !running() {
		t.Fatal("precondition: no isolation")
	}
	if err := e.s.ExtendSubscription(ctx, id, 5, 0); err != nil {
		t.Fatal(err)
	}
	if running() {
		t.Fatal("isolation still running after the admin extended the plan")
	}
}

// raceDev renews the customer while the expiry job's RemoveCustomer is in flight.
type raceDev struct {
	fakeDev
	hook func()
}

func (d raceDev) RemoveCustomer(ctx context.Context, c device.Customer, p device.Plan) error {
	d.hook()
	return d.fakeDev.RemoveCustomer(ctx, c, p)
}

// F5: a recharge between the job's re-read and its claim leaves an active sub off the router.
func TestAuditLifecycle5_ExpiryRechargeRace(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.now = e.now.AddDate(0, 0, 1).Add(time.Hour)
	fired := false
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) {
		return raceDev{fakeDev{calls: e.calls}, func() {
			if !fired {
				fired = true
				if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
					t.Error(err)
				}
			}
		}}, nil
	}
	*e.calls = nil
	e.s.ExpireDue(ctx)
	if e.sub(t).Status == "active" && (*e.calls)[len(*e.calls)-1] == "remove:day" {
		t.Fatalf("paid customer active in DB but last router op was remove: %v", *e.calls)
	}
}

type countFail struct {
	fakeDev
	n *int
}

func (d countFail) RemoveCustomer(context.Context, device.Customer, device.Plan) error {
	*d.n++
	return errors.New("dial tcp: i/o timeout")
}

// F6 / router #2: a dead router is dialed once per due subscription, no per-run breaker.
func TestAuditLifecycle6_DeadRouterNoBreaker(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	c2, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "U2", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.s.Recharge(ctx, c2.ID, e.day.ID, "Admin - Cash", 0)
	n := 0
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return countFail{fakeDev{calls: e.calls}, &n}, nil }
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	if n > 1 {
		t.Fatalf("dead router dialed %d times in one run (10s each in production)", n)
	}
}

// F7: Period extend on billing day 31 from Jan 31 skips February.
func TestAuditLifecycle7_PeriodDay31SkipsMonth(t *testing.T) {
	from := time.Date(2025, 1, 31, 23, 59, 0, 0, jkt)
	if got := NewExpiry(from, 1, Period, Options{Extend: true, BillingDay: 31}); got.Month() != time.February {
		t.Fatalf("1 period from Jan 31 = %v, skipped February", got)
	}
}

// F8: moving a plan to another router makes the next recharge a second active subscription.
func TestAuditLifecycle8_PlanRouterEditDoubleSub(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	rt2, err := e.q.CreateRouter(ctx, db.CreateRouterParams{Name: "r2", Host: "h2", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.conn.Exec("UPDATE plans SET router_id = ? WHERE id = ?", rt2.ID, e.day.ID); err != nil {
		t.Fatal(err)
	}
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	n := 0
	for _, s := range e.subs(t) {
		if s.Status == "active" && s.PlanID == e.day.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d active subscriptions of the same plan", n)
	}
}

// F10: a subscription an admin deactivated can be revived by the customer via portal extend.
func TestAuditLifecycle10_DeactivatedSelfRevive(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "extend_expired", Value: "yes"})
	e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "extend_days", Value: "3"})
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	id := e.sub(t).ID
	if err := e.s.DeactivateSubscription(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.ExtendExpired(ctx, e.cust.ID, id); err == nil {
		t.Fatal("customer revived an admin-deactivated subscription")
	}
}

// Router #8: customer delete commits before the router removal; a failure leaves an orphan secret.
func TestAuditRouter8_DeleteOrphan(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	n := 0
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return countFail{fakeDev{calls: e.calls}, &n}, nil }
	if err := e.s.DeleteCustomer(ctx, e.cust.ID); err == nil {
		t.Fatal("precondition: router failure not reported")
	}
	if _, err := e.q.GetCustomer(ctx, e.cust.ID); err != nil {
		t.Fatal("customer deleted from DB while still on the router (orphan keeps authenticating)")
	}
}

// Router #8: force deletes the row even when the router removal fails.
func TestAuditRouter8_DeleteForce(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	n := 0
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return countFail{fakeDev{calls: e.calls}, &n}, nil }
	if err := e.s.DeleteCustomerForce(ctx, e.cust.ID, true); err == nil {
		t.Fatal("router error not reported")
	}
	if _, err := e.q.GetCustomer(ctx, e.cust.ID); err == nil {
		t.Fatal("force delete kept the customer")
	}
}
