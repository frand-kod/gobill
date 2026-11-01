package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

// A dead router no longer blocks expiry: the row expires, the removal is queued and retried later.
func TestExpireQueuesRemovalAndRetryClears(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	n := 0
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return countFail{fakeDev{calls: e.calls}, &n}, nil }
	e.now = e.now.AddDate(0, 0, 2)
	e.s.ExpireDue(ctx)
	if subs := e.subs(t); len(subs) != 1 || subs[0].Status != "expired" {
		t.Fatalf("subscription not expired with the router down: %+v", subs)
	}
	if c, _ := e.q.CountRouterPending(ctx); c != 1 {
		t.Fatalf("pending = %d, want 1", c)
	}
	e.s.RetryPending(ctx) // still down: stays, attempts+1
	rows, _ := e.q.ListRouterPending(ctx)
	if len(rows) != 1 || rows[0].Attempts != 1 {
		t.Fatalf("after failed retry: %+v", rows)
	}
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return fakeDev{calls: e.calls}, nil }
	e.s.RetryPending(ctx) // inside the backoff: not tried
	if c, _ := e.q.CountRouterPending(ctx); c != 1 {
		t.Fatal("retried before the backoff")
	}
	e.now = e.now.Add(11 * time.Minute)
	e.s.RetryPending(ctx)
	if c, _ := e.q.CountRouterPending(ctx); c != 0 {
		t.Fatal("entry not cleared after a successful retry")
	}
}

// Deleting a router drops its leftover per-router settings and queue rows.
func TestRouterDeleteDropsSettings(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	id := e.day.RouterID.Int64
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	for _, k := range []string{"wg_pending_", "wg_prev_", "radius_mig_disabled_"} {
		e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: fmt.Sprint(k, id), Value: "x"})
	}
	if _, err := e.conn.Exec("DELETE FROM subscriptions; DELETE FROM transactions; DELETE FROM plans"); err != nil {
		t.Fatal(err)
	}
	if err := e.q.DeleteRouter(ctx, id); err != nil {
		t.Fatal(err)
	}
	var n int
	e.conn.QueryRow("SELECT COUNT(*) FROM settings WHERE key IN (?, ?, ?)", fmt.Sprint("wg_pending_", id), fmt.Sprint("wg_prev_", id), fmt.Sprint("radius_mig_disabled_", id)).Scan(&n)
	if n != 0 {
		t.Fatalf("%d settings rows left", n)
	}
}
