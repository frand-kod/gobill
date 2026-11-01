package db

import (
	"path/filepath"
	"testing"
)

// A binary must refuse a database migrated by a newer release.
func TestAuditOps15_RefuseNewerSchema(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("PRAGMA user_version = 99999"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(conn); err == nil {
		t.Fatal("Migrate accepted a schema newer than this binary knows")
	}
}
