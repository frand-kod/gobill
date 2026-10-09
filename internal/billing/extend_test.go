package billing

import (
	"context"
	"testing"
	"time"
)

// R1: extending an already-expired subscription restarts from now (PHP plan.php:1036), turns it
// active and re-adds the customer to the device; an unexpired one extends from its expiry.
func TestExtendExpiredRestartsFromNow(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	sub := e.sub(t)
	e.now = e.now.AddDate(0, 0, 30) // long expired
	if _, err := e.conn.Exec("UPDATE subscriptions SET status = 'expired'"); err != nil {
		t.Fatal(err)
	}
	*e.calls = nil
	if err := e.s.ExtendSubscription(ctx, sub.ID, 3, 0); err != nil {
		t.Fatal(err)
	}
	got := e.sub(t) // fails if not active again
	if want := e.now.AddDate(0, 0, 3).Unix(); got.ExpiresAt != want {
		t.Fatalf("expiry %v want %v", time.Unix(got.ExpiresAt, 0), time.Unix(want, 0))
	}
	if len(*e.calls) == 0 || (*e.calls)[len(*e.calls)-1] != "add:day" {
		t.Fatalf("customer not re-added to device: %v", *e.calls)
	}
}
