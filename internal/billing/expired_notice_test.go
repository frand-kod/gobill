package billing

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func setExpiredMinutes(t *testing.T, e *env, v string) {
	t.Helper()
	if err := e.q.UpsertSetting(context.Background(), db.UpsertSettingParams{Key: "expired_notify_minutes_before", Value: v}); err != nil {
		t.Fatal(err)
	}
}

// expired_notify_minutes_before = 5: one notice in the window, none on the next run, none at the
// real expiry, and the plan still ends.
func TestExpiredNoticeEarly(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.conn.Exec(`UPDATE customers SET phone = "08123456789"`)
	got := withNotify(t, e)
	setExpiredMinutes(t, e, "5")
	exp := time.Unix(e.sub(t).ExpiresAt, 0)

	e.now = exp.Add(-10 * time.Minute)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 0 {
		t.Fatalf("too early: %v", reqs)
	}
	e.now = exp.Add(-4 * time.Minute)
	e.s.ExpireDue(ctx)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 1 {
		t.Fatalf("early notice: %v", reqs)
	}
	if s := e.sub(t); !s.ExpiredNotifiedAt.Valid || s.Status != "active" {
		t.Fatalf("claimed early, still active: %+v", s)
	}

	e.now = exp.Add(time.Minute)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 0 || count(reqs, "recharge.expired") != 1 {
		t.Fatalf("at expiry: %v", reqs)
	}
	if s := e.subs(t)[0]; s.Status != "expired" {
		t.Fatalf("plan must still end: %+v", s)
	}
}

// Extending after the early notice starts a new period, which notifies again at its own time.
func TestExpiredNoticeRenewNotifiesAgain(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.conn.Exec(`UPDATE customers SET phone = "08123456789"`)
	got := withNotify(t, e)
	setExpiredMinutes(t, e, "5")
	exp := time.Unix(e.sub(t).ExpiresAt, 0)

	e.now = exp.Add(-4 * time.Minute)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 1 {
		t.Fatalf("first period: %v", reqs)
	}
	if err := e.s.ExtendSubscription(ctx, e.sub(t).ID, 1, 0); err != nil {
		t.Fatal(err)
	}
	drain(got)
	if s := e.sub(t); s.ExpiredNotifiedAt.Valid {
		t.Fatalf("renewal must reset the notice: %+v", s)
	}
	newExp := time.Unix(e.sub(t).ExpiresAt, 0)

	e.now = exp.Add(time.Hour) // still a day from the new end
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 0 {
		t.Fatalf("new period notified too early: %v", reqs)
	}
	e.now = newExp.Add(-4 * time.Minute)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 1 {
		t.Fatalf("new period notice: %v", reqs)
	}
}

// 0 (the default): the notice goes out at expiry only, and the period is marked notified.
func TestExpiredNoticeAtExpiryWhenZero(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.conn.Exec(`UPDATE customers SET phone = "08123456789"`)
	got := withNotify(t, e)
	exp := time.Unix(e.sub(t).ExpiresAt, 0)

	e.now = exp.Add(-4 * time.Minute)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 0 {
		t.Fatalf("notified before expiry with 0: %v", reqs)
	}
	e.now = exp.Add(time.Minute)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 1 {
		t.Fatalf("at expiry: %v", reqs)
	}
	if s := e.subs(t)[0]; s.Status != "expired" || !s.ExpiredNotifiedAt.Valid {
		t.Fatalf("expired and marked: %+v", s)
	}
}

// A start_on_first_login subscription (pending_start = 1) never gets an early notice.
func TestExpiredNoticePendingStartNotEarly(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	setExpiredMinutes(t, e, "5")
	got := withNotify(t, e)
	c, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "pend", PasswordHash: "h", Fullname: "P", Phone: "08123456789", ServiceType: "PPPoE", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	exp := e.now.Add(3 * time.Minute)
	if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: e.day.ID, RouterID: e.day.RouterID,
		Type: "PPPoE", StartedAt: e.now.Unix(), ExpiresAt: exp.Unix(), Method: "x", PendingStart: 1}); err != nil {
		t.Fatal(err)
	}
	e.s.ExpireDue(ctx)
	e.now = exp.Add(time.Minute)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/wa") != 0 {
		t.Fatalf("pending notified: %v", reqs)
	}
	subs, err := e.q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if subs[0].ExpiredNotifiedAt != (sql.NullInt64{}) {
		t.Fatalf("pending row claimed: %+v", subs[0])
	}
}
