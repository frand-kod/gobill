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
	b := &Backup{Conn: conn, Q: q, Dir: backupDir, Trusted: func() bool { return trusted }}

	// Untrusted clock at 02:00: nothing is written.
	trusted = false
	b.Now = func() time.Time { return at(1, 2) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "nuxbill-20261001.db")); !os.IsNotExist(err) {
		t.Fatalf("untrusted run wrote a backup: %v", err)
	}

	// Trusted clock, outside the hour: nothing is written.
	trusted = true
	b.Now = func() time.Time { return at(1, 3) }
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "nuxbill-20261001.db")); !os.IsNotExist(err) {
		t.Fatalf("run outside 02:00 wrote a backup")
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
	if len(names) != 2 || names[0] != "nuxbill-20261002.db" || names[1] != "nuxbill-20261003.db" {
		t.Fatalf("rotation kept %v, want the two newest", names)
	}

	// The copy is a real database with the same schema.
	copyConn, err := db.Open(filepath.Join(backupDir, "nuxbill-20261003.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer copyConn.Close()
	if _, err := db.New(copyConn).CountCustomers(ctx); err != nil {
		t.Fatalf("backup is not usable: %v", err)
	}

	// Today's file already exists: the run must not overwrite it.
	marker := filepath.Join(backupDir, "nuxbill-20261003.db")
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
