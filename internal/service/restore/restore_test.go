package restore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestBackupThenInspect(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := Backup(ctx, conn, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Inspect(ctx, path, []byte("0123456789abcdef0123456789abcdef"), time.UTC); err != nil {
		t.Fatalf("own backup refused: %v %#v", err, err)
	}
	cleanup()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("cleanup left the copy")
	}
}

func TestInspectRefusesGarbage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.db")
	os.WriteFile(p, []byte("not sqlite at all"), 0o600)
	var rf *Refusal
	if _, _, _, err := Inspect(context.Background(), p, nil, time.UTC); !errors.As(err, &rf) {
		t.Fatalf("want Refusal, got %v", err)
	}
}
