package billing

import (
	"context"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// R4: data usage is counted from started_at, so bringing a dead subscription back (admin extend,
// edit, portal self-extend) must restart the window; extending a live one must not.
func TestReviveRestartsUsageWindow(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "s1", Username: e.cust.Username, NasIp: "1.1.1.1",
		StartedAt: e.now.Unix(), UpdatedAt: e.now.Unix(), InputOctets: 700, OutputOctets: 300}); err != nil {
		t.Fatal(err)
	}
	used := func() int64 {
		n, _ := e.q.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: e.cust.Username, StartedAt: e.sub(t).StartedAt})
		return n
	}
	sub := e.sub(t)
	if used() != 1000 {
		t.Fatalf("usage %d", used())
	}
	// live extension keeps the usage
	e.now = e.now.Add(time.Hour)
	if err := e.s.ExtendSubscription(ctx, sub.ID, 3, 0); err != nil {
		t.Fatal(err)
	}
	if used() != 1000 {
		t.Fatalf("live extend reset usage: %d", used())
	}
	// expired, then extended: new window
	e.now = e.now.AddDate(0, 0, 30)
	if _, err := e.conn.Exec("UPDATE subscriptions SET status = 'expired'"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.ExtendSubscription(ctx, sub.ID, 3, 0); err != nil {
		t.Fatal(err)
	}
	if used() != 0 || e.sub(t).StartedAt != e.now.Unix() {
		t.Fatalf("revive: usage %d started %d", used(), e.sub(t).StartedAt)
	}
}
