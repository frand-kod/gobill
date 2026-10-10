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
	at := func(day, hour, min int) time.Time { return time.Date(2026, 10, day, hour, min, 0, 0, time.Local) }
	b := &Backup{Conn: conn, Q: q, Dir: backupDir, Mirror: mirrorDir, Trusted: func() bool { return true }}

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
	status := func() map[string]string {
		st, err := b.settings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	// Days 1 to 3: both folders keep the newest 2, and no error is recorded.
	for day := 1; day <= 3; day++ {
		b.Now = func() time.Time { return at(day, 2, 0) }
		if err := b.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want(backupDir, "nuxbill-20261002.db", "nuxbill-20261003.db")
	want(mirrorDir, "nuxbill-20261002.db", "nuxbill-20261003.db")
	if st := status(); st["backup_mirror_error"] != "" {
		t.Fatalf("error while healthy: %v", st)
	}

	// Mirror folder gone (unmounted): the local backup still succeeds and the error is recorded.
	if err := os.RemoveAll(mirrorDir); err != nil {
		t.Fatal(err)
	}
	b.Now = func() time.Time { return at(4, 2, 0) }
	if err := b.Run(ctx); err != nil {
		t.Fatalf("local backup failed with the mirror down: %v", err)
	}
	want(backupDir, "nuxbill-20261003.db", "nuxbill-20261004.db")
	if st := status(); st["backup_mirror_error"] == "" || st["backup_last_file"] != "nuxbill-20261004.db" {
		t.Fatalf("status not recorded: %v", st)
	}

	// Same hour: no second attempt. One hour later the retry runs but the mirror is still down.
	b.Now = func() time.Time { return at(4, 2, 30) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status()["backup_mirror_try_at"]; got != "2026-10-04 02:00:00" {
		t.Fatalf("attempt repeated within the hour: try_at %q", got)
	}
	b.Now = func() time.Time { return at(4, 3, 1) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status()["backup_mirror_try_at"]; got != "2026-10-04 03:01:00" {
		t.Fatalf("hourly retry did not run: try_at %q", got)
	}

	// Mirror back on day 5: the copy is made and the error is cleared.
	if err := os.Mkdir(mirrorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	b.Now = func() time.Time { return at(5, 2, 0) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	want(mirrorDir, "nuxbill-20261005.db")
	st := status()
	if st["backup_mirror_error"] != "" || st["backup_mirror_at"] == "" {
		t.Fatalf("recovery not recorded: %v", st)
	}
}

// A failed mirror copy is retried hourly the same day and then stops retrying once it works.
func TestBackupMirrorRetrySucceeds(t *testing.T) {
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
	mirrorDir := filepath.Join(dir, "mirror") // missing at first
	b := &Backup{Conn: conn, Q: db.New(conn), Dir: filepath.Join(dir, "backup"), Mirror: mirrorDir,
		Trusted: func() bool { return true }}
	at := func(hour, min int) time.Time { return time.Date(2026, 10, 9, hour, min, 0, 0, time.Local) }

	b.Now = func() time.Time { return at(2, 0) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mirrorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// 02:30 is too early for a retry; 03:00 succeeds.
	b.Now = func() time.Time { return at(2, 30) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mirrorDir, "nuxbill-20261009.db")); err == nil {
		t.Fatal("copy made before the hourly retry")
	}
	b.Now = func() time.Time { return at(3, 0) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mirrorDir, "nuxbill-20261009.db")); err != nil {
		t.Fatalf("retry did not copy the backup: %v", err)
	}
	st, err := b.settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st["backup_mirror_error"] != "" {
		t.Fatalf("error not cleared: %v", st)
	}
}
