package billing

import (
	"database/sql"
	"fmt"
	"time"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskAlertOncePerCrossing(t *testing.T) {
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	var mb int64
	var ferr error
	job := &AlertJob{S: e.s, Free: func() (int64, error) { return mb, ferr }}
	check := func(free int64, want int, what string) {
		t.Helper()
		mb = free
		if err := job.Run(ctx); err != nil {
			t.Fatal(err)
		}
		// only the disk messages: other rules may send their own alerts in the same process
		if n := count(drain(got), "database"); n != want {
			t.Fatalf("%s: %d alerts, want %d", what, n, want)
		}
	}
	check(500, 0, "plenty of space")
	check(150, 1, "drops below 200 MB")
	check(100, 0, "still low")
	check(300, 1, "recovered")
	check(300, 0, "still fine")

	ferr = errors.New("statfs failed")
	if err := job.Run(ctx); err == nil {
		t.Fatal("free-space error not returned")
	}
}

func TestStartMarkerAlertsOnStale(t *testing.T) {
	e := setup(t)
	got := withNotify(t, e)
	path := filepath.Join(t.TempDir(), ".running")

	// stale marker from a crash: alert, and the marker is written again
	if err := os.WriteFile(path, []byte("running\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.s.StartMarker(path); err != nil {
		t.Fatal(err)
	}
	if reqs := drain(got); count(reqs, "sendMessage") != 1 || count(reqs, "dimulai+ulang") != 1 {
		t.Fatalf("stale marker: want one restart alert, got %v", reqs)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("marker not recreated:", err)
	}

	// clean stop removes the marker; the next start is silent
	os.Remove(path)
	if err := e.s.StartMarker(path); err != nil {
		t.Fatal(err)
	}
	if n := count(drain(got), "sendMessage"); n != 0 {
		t.Fatalf("clean start: %d alerts, want 0", n)
	}
}

func TestMassDropAlert(t *testing.T) {
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	now := e.now.Unix()
	stop := func(ip, user string, ago int64) {
		t.Helper()
		if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: user, Username: user, NasIp: ip,
			StartedAt: now - 7200, UpdatedAt: now - ago, StoppedAt: sql.NullInt64{Int64: now - ago, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	job := &AlertJob{S: e.s, Free: func() (int64, error) { return 500, nil }, Now: func() time.Time { return e.now }}
	run := func(want int, what string) {
		t.Helper()
		if err := job.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(drain(got), "massal"); n != want {
			t.Fatalf("%s: %d alerts, want %d", what, n, want)
		}
	}
	for i := 0; i < 4; i++ {
		stop("10.0.0.1", fmt.Sprintf("u%d", i), 60)
	}
	stop("10.0.0.1", "old", 3*3600) // outside the window
	run(0, "below threshold")
	stop("10.0.0.1", "u4", 60)
	run(1, "threshold met")
	run(0, "same outage, next tick")

	for i := 0; i < 5; i++ { // whole NAS silent: left to the NAS-silent rule
		stop("10.0.0.2", fmt.Sprintf("s%d", i), 60)
	}
	old := metrics.Now
	metrics.Now = func() time.Time { return e.now }
	defer func() { metrics.Now = old }()
	metrics.Set("radius_nas_last_packet_timestamp", float64(e.now.Add(-time.Hour).Unix()), "nas", "10.0.0.2")
	run(0, "silent NAS")
}

// The portal banner: only customers of a mass drop that has reached the threshold, and only while offline.
func TestMassDropNow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	now := e.now.Unix()
	stop := func(ip, user string, ago int64) {
		t.Helper()
		if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: user, Username: user, NasIp: ip,
			StartedAt: now - 7200, UpdatedAt: now - ago, StoppedAt: sql.NullInt64{Int64: now - ago, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		stop("10.0.0.1", fmt.Sprintf("u%d", i), 60)
	}
	stop("10.0.0.1", "old", 3*3600) // outside the window
	st := map[string]string{"alert_mass_drop_min": "5", "alert_mass_drop_window": "30"}
	covered := func(st map[string]string, user string) bool {
		t.Helper()
		got, err := MassDropNow(ctx, e.q, st, user, e.now)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if !covered(st, "u0") {
		t.Fatal("u0 is one of 5 dropped customers: want the banner")
	}
	if covered(st, "old") {
		t.Fatal("old dropped outside the window: no banner")
	}
	if covered(map[string]string{"alert_mass_drop_min": "6", "alert_mass_drop_window": "30"}, "u0") {
		t.Fatal("below the threshold: no banner")
	}
	if covered(map[string]string{"alert_mass_drop_min": "0"}, "u0") {
		t.Fatal("rule off: no banner")
	}
	// u0 is back online: an open session ends the drop for that customer
	if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "u0-new", Username: "u0", NasIp: "10.0.0.1",
		StartedAt: now - 30, UpdatedAt: now - 30}); err != nil {
		t.Fatal(err)
	}
	if covered(st, "u0") {
		t.Fatal("u0 came back online: no banner")
	}
}
