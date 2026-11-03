package billing

import (
	"context"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Portal emergency extension is free, but the next purchase counts from the original expiry.
// emergency makes a 30-day sub that expired 3 days ago and was then extended; it returns the original expiry.
func (e *env) emergency(t *testing.T) time.Time {
	t.Helper()
	ctx := context.Background()
	e.conn.Exec("UPDATE plans SET validity = 30")
	set(t, e, "extend_expired", "1", "extend_days", "3")
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	sub := e.sub(t)
	orig := time.Unix(sub.ExpiresAt, 0)
	e.now = orig.AddDate(0, 0, 3)
	e.q.ExpireSubscription(ctx, db.ExpireSubscriptionParams{ID: sub.ID, Now: 1 << 40})
	if _, err := e.s.ExtendExpired(ctx, e.cust.ID, sub.ID); err != nil {
		t.Fatal(err)
	}
	return orig
}

func TestEmergencyPaidDuringExtension(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	orig := e.emergency(t)
	e.now = e.now.AddDate(0, 0, 1) // still inside the 3 free days
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if got, want := e.sub(t).ExpiresAt, orig.AddDate(0, 0, 30).Unix(); got != want {
		t.Fatalf("expiry %v want %v (original expiry + 30d)", time.Unix(got, 0), time.Unix(want, 0))
	}
	// settled: the next purchase extends from the new expiry as usual
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if got, want := e.sub(t).ExpiresAt, orig.AddDate(0, 0, 60).Unix(); got != want {
		t.Fatalf("second purchase %v want %v", time.Unix(got, 0), time.Unix(want, 0))
	}
}

func TestEmergencyPaidAfterExtensionPassed(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	orig := e.emergency(t)
	e.now = e.now.AddDate(0, 0, 5) // the 3 days ran out
	e.conn.Exec("UPDATE subscriptions SET status = 'expired'")
	pv, err := e.s.Preview(ctx, e.cust.ID, e.day.ID)
	if err != nil || !pv.Expiry.Equal(orig.AddDate(0, 0, 30)) {
		t.Fatalf("preview %v %v", pv.Expiry, err)
	}
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if got, want := e.sub(t).ExpiresAt, orig.AddDate(0, 0, 30).Unix(); got != want {
		t.Fatalf("expiry %v want %v", time.Unix(got, 0), time.Unix(want, 0))
	}
}

func TestEmergencyDifferentPlanOrTooOld(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.emergency(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day2.ID, "Admin - Cash", 0); err != nil { // other plan: from now, mark settled
		t.Fatal(err)
	}
	if got, want := e.sub(t).ExpiresAt, e.now.AddDate(0, 0, 30).Unix(); got != want {
		t.Fatalf("other plan %v want %v", time.Unix(got, 0), time.Unix(want, 0))
	}
	// so much time passed that counting from the original expiry leaves under 24h: count from now
	e2 := setup(t)
	e2.emergency(t)
	e2.now = e2.now.AddDate(0, 0, 40)
	e2.conn.Exec("UPDATE subscriptions SET status = 'expired'")
	if err := e2.s.Recharge(ctx, e2.cust.ID, e2.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if got, want := e2.sub(t).ExpiresAt, e2.now.AddDate(0, 0, 30).Unix(); got != want {
		t.Fatalf("too old %v want %v", time.Unix(got, 0), time.Unix(want, 0))
	}
}
