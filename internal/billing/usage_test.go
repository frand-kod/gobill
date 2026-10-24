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

// An early renewal of a limited plan keeps the usage window and rolls the data over (old remaining +
// one full limit, an overspend forgiven); a renewal after expiry, a plan change and a deactivation
// start from nothing.
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
	left := func() int64 {
		t.Helper()
		sub := e.sub(t)
		used, _, err := db.LoginUsage(ctx, e.q, e.cust, sub.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		return db.QuotaBytes(100, "GB", sub.QuotaCarryBytes) - used
	}
	recharge()
	started := e.sub(t).StartedAt
	use("s1", 50*gb)
	e.now = e.now.Add(time.Hour)
	recharge() // early: 50 GB left + a full 100 GB
	if got := left(); got != 150*gb {
		t.Fatalf("early renewal remaining %d want %d", got, 150*gb)
	}
	if e.sub(t).StartedAt != started {
		t.Fatal("early renewal reset the usage window")
	}
	// the session stays open across the renewal: its later octets still count
	use("s1", 70*gb)
	if got := left(); got != 130*gb {
		t.Fatalf("open session after renewal remaining %d want %d", got, 130*gb)
	}
	use("s1", 255*gb) // overspent by 105 GB over the 150 GB quota
	e.now = e.now.Add(time.Hour)
	recharge() // overspend forgiven: exactly one full limit left
	if got := left(); got != 100*gb {
		t.Fatalf("overspent renewal remaining %d want %d", got, 100*gb)
	}
	use("s2", 10*gb)
	e.now = e.now.Add(time.Hour)
	recharge() // stacks: 90 GB left + 100 GB
	if got := left(); got != 190*gb {
		t.Fatalf("stacked remaining %d want %d", got, 190*gb)
	}
	// expired, then renewed: fresh quota and window
	e.now = e.now.AddDate(0, 0, 30)
	if _, err := e.conn.Exec("UPDATE subscriptions SET status = 'expired'"); err != nil {
		t.Fatal(err)
	}
	recharge()
	if sub := e.sub(t); left() != 100*gb || sub.QuotaCarryBytes != 0 || sub.StartedAt != e.now.Unix() {
		t.Fatalf("renewal after expiry: remaining %d carry %d started %d", left(), sub.QuotaCarryBytes, sub.StartedAt)
	}
	// carry again, then a plan change clears it
	e.now = e.now.Add(time.Hour)
	recharge()
	if got := left(); got != 200*gb {
		t.Fatalf("carry before plan change %d want %d", got, 200*gb)
	}
	if err := e.s.EditSubscription(ctx, e.sub(t).ID, e.day2.ID, e.now.AddDate(0, 0, 5), 0); err != nil {
		t.Fatal(err)
	}
	if c := e.sub(t).QuotaCarryBytes; c != 0 {
		t.Fatalf("plan change kept carry %d", c)
	}
	// deactivate clears it too
	if _, err := e.conn.Exec("UPDATE subscriptions SET quota_carry_bytes = 5, quota_carry_seconds = 5"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeactivateSubscription(ctx, e.sub(t).ID, 0); err != nil {
		t.Fatal(err)
	}
	if subs := e.subs(t); subs[0].QuotaCarryBytes != 0 || subs[0].QuotaCarrySeconds != 0 {
		t.Fatalf("deactivate kept carry %d/%d", subs[0].QuotaCarryBytes, subs[0].QuotaCarrySeconds)
	}
}

// Time does not roll over: after an early renewal exactly the full time limit is left, not the
// leftover plus another limit.
func TestLimitedRenewalTimeDoesNotRollOver(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if _, err := e.conn.Exec("UPDATE plans SET limited = 1, limit_type = 'Time_Limit', time_limit = 60, time_unit = 'Mins' WHERE id = ?", e.day.ID); err != nil {
		t.Fatal(err)
	}
	recharge := func() {
		t.Helper()
		if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
			t.Fatal(err)
		}
	}
	remaining := func() int64 {
		t.Helper()
		sub := e.sub(t)
		_, secs, err := db.LoginUsage(ctx, e.q, e.cust, sub.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		return db.QuotaSeconds(60, "Mins", sub.QuotaCarrySeconds) - secs
	}
	recharge()
	start := e.now.Unix()
	if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "t1", Username: e.cust.Username, NasIp: "1.1.1.1",
		StartedAt: start, UpdatedAt: start + 40*60}); err != nil {
		t.Fatal(err)
	}
	if got := remaining(); got != 20*60 {
		t.Fatalf("before renewal %d", got)
	}
	e.now = e.now.Add(time.Hour)
	recharge()
	if got := remaining(); got != 60*60 {
		t.Fatalf("after early renewal %d want 3600", got)
	}
	if e.sub(t).QuotaCarryBytes != 0 {
		t.Fatal("time plan carried data")
	}
	// the open session keeps counting after the renewal
	if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "t1", Username: e.cust.Username, NasIp: "1.1.1.1",
		StartedAt: start, UpdatedAt: start + 50*60}); err != nil {
		t.Fatal(err)
	}
	if got := remaining(); got != 50*60 {
		t.Fatalf("open session after renewal %d want 3000", got)
	}
	// a second early renewal again leaves a full hour
	e.now = e.now.Add(time.Hour)
	recharge()
	if got := remaining(); got != 60*60 {
		t.Fatalf("second renewal %d want 3600", got)
	}
}
