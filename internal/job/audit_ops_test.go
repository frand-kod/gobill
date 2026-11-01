package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// A panicking job must not take the whole process down.
func TestAuditOps1_RunRecoversPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		Run(ctx, "audit-panic", time.Millisecond, func(context.Context) error { panic("boom") })
	}()
	if p := <-panicked; p != nil {
		t.Fatalf("job.Run let a job panic escape: %v", p)
	}
}

// Box off during 02:xx: a later trusted tick the same day should still back up.
func TestAuditOps7_BackupCatchesUpAfterHour(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	b := &Backup{Conn: conn, Q: db.New(conn), Dir: filepath.Join(dir, "backup"), Trusted: func() bool { return true },
		Now: func() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local) }}
	if err := b.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "gobill-20261001.db")); err != nil {
		t.Fatalf("no backup taken after missing the 02:00 hour: %v", err)
	}
}
