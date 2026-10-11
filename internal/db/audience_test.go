package db

import (
	"path/filepath"
	"testing"
)

// Migration 0017: existing and plain-SQL plans are public; only public and admin are accepted.
func TestPlanAudienceMigration(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO plans (name, type, price, validity, validity_unit) VALUES ('x', 'Balance', 1, 1, 'Days')`); err != nil {
		t.Fatal(err)
	}
	var aud string
	if err := conn.QueryRow(`SELECT audience FROM plans WHERE name = 'x'`).Scan(&aud); err != nil || aud != "public" {
		t.Fatalf("default audience %q: %v", aud, err)
	}
	if _, err := conn.Exec(`INSERT INTO plans (name, type, price, validity, validity_unit, audience) VALUES ('y', 'Balance', 1, 1, 'Days', 'vip')`); err == nil {
		t.Fatal("audience outside public/admin accepted")
	}
}
