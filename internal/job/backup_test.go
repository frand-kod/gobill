package job

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestBackupWritesRotatesAndSkips(t *testing.T) {
	ctx := t.Context()
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
	at := func(day int, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, time.Local) }
	trusted := true
	b := &Backup{Conn: conn, Q: q, Dir: backupDir, Trusted: func() bool { return trusted }, KeyFP: "0a1b2c3d"}

	// Untrusted clock at 02:00: nothing is written.
	trusted = false
	b.Now = func() time.Time { return at(1, 2) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "gobill-20261001.db")); !os.IsNotExist(err) {
		t.Fatalf("untrusted run wrote a backup: %v", err)
	}

	// Trusted clock, before the backup hour: nothing is written.
	trusted = true
	b.Now = func() time.Time { return at(1, 1) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "gobill-20261001.db")); !os.IsNotExist(err) {
		t.Fatalf("run before 02:00 wrote a backup")
	}

	// Days 1 to 3 at 02:00: the file is created, and rotation keeps only the newest 2.
	for day := 1; day <= 3; day++ {
		b.Now = func() time.Time { return at(day, 2) }
		if err := b.Run(ctx); err != nil {
			t.Fatal(err)
		}
		// A second tick in the same hour must not touch today's file.
		if err := b.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != "gobill-20261002.db" || names[1] != "gobill-20261003.db" {
		t.Fatalf("rotation kept %v, want the two newest", names)
	}

	// The copy is a real database with the same schema.
	copyConn, err := db.Open(filepath.Join(backupDir, "gobill-20261003.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer copyConn.Close()
	if _, err := db.New(copyConn).CountCustomers(ctx); err != nil {
		t.Fatalf("backup is not usable: %v", err)
	}

	// The copy carries the key fingerprint, not the key.
	var fp string
	if err := copyConn.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'key_fp'").Scan(&fp); err != nil || fp != "0a1b2c3d" {
		t.Fatalf("backup key_fp = %q, %v", fp, err)
	}

	// Today's file already exists: the run must not overwrite it.
	marker := filepath.Join(backupDir, "gobill-20261003.db")
	if err := os.WriteFile(marker, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(marker); string(got) != "keep me" {
		t.Fatal("existing backup was overwritten")
	}
}

// Snapshots older than 30 days go, but the newest 3 of each label stay whatever their age.
func TestPruneSnapshotsKeepsNewestThreePerLabel(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)
	deleted := map[string]bool{
		"gobill-20260901-101010-pre-update.db": true, // the two oldest pre-update snapshots, beyond the newest 3
		"gobill-20260902-101010-pre-update.db": true,
	}
	all := []string{
		"gobill-20260903-101010-pre-update.db",
		"gobill-20260904-101010-pre-update.db",
		"gobill-20260905-101010-pre-update.db",
		"gobill-20261101-101010-pre-import.db",  // recent
		"gobill-20260901-101010-pre-restore.db", // the only one of its label
		"gobill-20260901-101010-other.db",       // not a snapshot
	}
	for n := range deleted {
		all = append(all, n)
	}
	for _, n := range all {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneSnapshots(dir, now); err != nil {
		t.Fatal(err)
	}
	for _, n := range all {
		_, err := os.Stat(filepath.Join(dir, n))
		if gone := os.IsNotExist(err); gone != deleted[n] {
			t.Errorf("%s: gone=%v, want %v", n, gone, deleted[n])
		}
	}
}
