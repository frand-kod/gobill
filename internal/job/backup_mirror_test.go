package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestBackupMirror(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "backup_keep", Value: "2"}); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backup")
	mirrorDir := filepath.Join(dir, "mirror")
	if err := os.Mkdir(mirrorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var alerts []string
	at := func(day int) time.Time { return time.Date(2026, 10, day, 2, 0, 0, 0, time.Local) }
	b := &Backup{Conn: conn, Q: q, Dir: backupDir, Mirror: mirrorDir, Trusted: func() bool { return true },
		Alert: func(_ context.Context, msg string) error { alerts = append(alerts, msg); return nil }}

	names := func(d string) []string {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	want := func(d string, w ...string) {
		t.Helper()
		got := names(d)
		if len(got) != len(w) || (len(w) == 2 && (got[0] != w[0] || got[1] != w[1])) {
			t.Fatalf("%s holds %v, want %v", d, got, w)
		}
	}

	// Days 1 to 3: both folders keep the newest 2, and no alert is sent.
	for day := 1; day <= 3; day++ {
		b.Now = func() time.Time { return at(day) }
		if err := b.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want(backupDir, "nuxbill-20261002.db", "nuxbill-20261003.db")
	want(mirrorDir, "nuxbill-20261002.db", "nuxbill-20261003.db")
	if len(alerts) != 0 {
		t.Fatalf("alerts while healthy: %v", alerts)
	}

	// Mirror folder gone (unmounted): the local backup still succeeds and one alert is sent.
	if err := os.RemoveAll(mirrorDir); err != nil {
		t.Fatal(err)
	}
	b.Now = func() time.Time { return at(4) }
	if err := b.Run(ctx); err != nil {
		t.Fatalf("local backup failed with the mirror down: %v", err)
	}
	want(backupDir, "nuxbill-20261003.db", "nuxbill-20261004.db")
	if len(alerts) != 1 {
		t.Fatalf("alerts after failure: %v, want one", alerts)
	}
	// Same day again: no second alert.
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alert repeated on the same day: %v", alerts)
	}
	st, err := b.settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st["backup_mirror_error"] == "" || st["backup_last_file"] != "nuxbill-20261004.db" {
		t.Fatalf("status not recorded: %v", st)
	}

	// Mirror back on day 5: the copy is made, and one recovery alert is sent.
	if err := os.Mkdir(mirrorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	b.Now = func() time.Time { return at(5) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	want(mirrorDir, "nuxbill-20261005.db")
	if len(alerts) != 2 {
		t.Fatalf("alerts after recovery: %v, want two", alerts)
	}
	st, err = b.settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st["backup_mirror_error"] != "" || st["backup_mirror_at"] == "" {
		t.Fatalf("recovery not recorded: %v", st)
	}
}
