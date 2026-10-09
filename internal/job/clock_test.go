package job

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func newGuard(t *testing.T, now int64, synced bool) *ClockGuard {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	g := NewClockGuard(db.New(conn))
	g.Now = func() time.Time { return time.Unix(now, 0) }
	g.Synced = func() bool { return synced }
	return g
}

func set(t *testing.T, g *ClockGuard, k, v string) {
	t.Helper()
	if err := g.Q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
		t.Fatal(err)
	}
}

func lastSeen(t *testing.T, g *ClockGuard) string {
	t.Helper()
	rows, _ := g.Q.ListSettings(t.Context())
	for _, r := range rows {
		if r.Key == keyLastSeen {
			return r.Value
		}
	}
	return ""
}

func TestClockGuard(t *testing.T) {
	tests := []struct {
		name    string
		now     int64
		synced  bool
		guard   string
		trusted bool
		want    string // last_seen_at after Check
	}{
		{"backwards", 100, true, "", false, "500"},
		{"forward", 900, true, "", true, "900"},
		{"unsync", 900, false, "", false, "500"},
		{"off ignores everything", 100, false, "off", true, "500"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newGuard(t, tc.now, tc.synced)
			set(t, g, keyLastSeen, "500")
			if tc.guard != "" {
				set(t, g, keyGuard, tc.guard)
			}
			if err := g.Check(t.Context()); err != nil {
				t.Fatal(err)
			}
			if g.Trusted() != tc.trusted || (g.Reason() == "") != tc.trusted {
				t.Fatalf("trusted=%v reason=%q", g.Trusted(), g.Reason())
			}
			if got := lastSeen(t, g); got != tc.want {
				t.Fatalf("last_seen_at=%s want %s", got, tc.want)
			}
		})
	}
}

func TestClockGuardFirstRun(t *testing.T) {
	g := newGuard(t, 42, true)
	if err := g.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !g.Trusted() || lastSeen(t, g) != strconv.Itoa(42) {
		t.Fatal("first run should trust and persist")
	}
}
