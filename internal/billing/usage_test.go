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

// An early renewal of a limited plan restarts the usage window and rolls the unused data over;
// a renewal after expiry, a plan change and a deactivation start from nothing.
func TestLimitedRenewalCarriesUnusedQuota(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	const gb = int64(1) << 30
	if _, err := e.conn.Exec("UPDATE plans SET limited = 1, limit_type = 'Data_Limit', data_limit = 100, data_unit = 'GB' WHERE id = ?", e.day.ID); err != nil {
		t.Fatal(err)
	}
	recharge := func() {
		t.Helper()
		if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
			t.Fatal(err)
		}
	}
	use := func(id string, n int64) {
		t.Helper()
		if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: id, Username: e.cust.Username, NasIp: "1.1.1.1",
			StartedAt: e.now.Unix(), UpdatedAt: e.now.Unix(), InputOctets: n}); err != nil {
			t.Fatal(err)
		}
	}
	total := func() int64 {
		pl, _ := e.q.GetRadiusPlan(ctx, e.cust.ID)
		return db.QuotaBytes(100, "GB", pl.QuotaCarryBytes)
	}
	recharge()
	use("s1", 50*gb)
	e.now = e.now.Add(time.Hour)
	recharge() // early: 100 GB + 50 GB unused
	if got := total(); got != 150*gb {
		t.Fatalf("early renewal total %d want %d", got, 150*gb)
	}
	use("s2", 200*gb) // overspent: nothing to carry, never negative
	e.now = e.now.Add(time.Hour)
	recharge()
	if got := total(); got != 100*gb {
		t.Fatalf("overspent renewal total %d want %d", got, 100*gb)
	}
	use("s3", 10*gb)
	e.now = e.now.Add(time.Hour)
	recharge()
	if got := total(); got != 190*gb {
		t.Fatalf("carry total %d want %d", got, 190*gb)
	}
	// expired, then renewed: fresh quota
	e.now = e.now.AddDate(0, 0, 30)
	if _, err := e.conn.Exec("UPDATE subscriptions SET status = 'expired'"); err != nil {
		t.Fatal(err)
	}
	recharge()
	if got := total(); got != 100*gb {
		t.Fatalf("renewal after expiry total %d want %d", got, 100*gb)
	}
	// carry again, then a plan change clears it
	e.now = e.now.Add(time.Hour)
	recharge()
	if got := total(); got != 200*gb {
		t.Fatalf("carry before plan change %d want %d", got, 200*gb)
	}
	if err := e.s.EditSubscription(ctx, e.sub(t).ID, e.day2.ID, e.now.AddDate(0, 0, 5), 0); err != nil {
		t.Fatal(err)
	}
	if c := e.sub(t).QuotaCarryBytes; c != 0 {
		t.Fatalf("plan change kept carry %d", c)
	}
	// deactivate clears it too
	if _, err := e.conn.Exec("UPDATE subscriptions SET quota_carry_bytes = 5"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeactivateSubscription(ctx, e.sub(t).ID, 0); err != nil {
		t.Fatal(err)
	}
	if subs := e.subs(t); subs[0].QuotaCarryBytes != 0 {
		t.Fatalf("deactivate kept carry %d", subs[0].QuotaCarryBytes)
	}
}
