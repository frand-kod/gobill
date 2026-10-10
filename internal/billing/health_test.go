package billing

import (
	"context"
	"errors"
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
