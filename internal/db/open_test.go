package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateIsIdempotent(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	for i := 0; i < 2; i++ {
		if err := Migrate(conn); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	var version int
	conn.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 4 {
		t.Fatalf("user_version = %d, want 4", version)
	}

	var fk int
	conn.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Fatal("foreign_keys pragma not enabled")
	}

	q := New(conn)
	if _, err := q.CreateAdmin(context.Background(), CreateAdminParams{
		Username: "a", PasswordHash: "x", Role: "Root",
	}); err == nil {
		t.Fatal("role CHECK constraint not enforced")
	}
}
