package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretSettings(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := New(conn)
	SettingsKey = []byte(strings.Repeat("k", 32))
	t.Cleanup(func() { SettingsKey = nil })
	raw := func(k string) string {
		var v string
		conn.QueryRow(`SELECT value FROM settings WHERE key = ?`, k).Scan(&v)
		return v
	}
	get := func(k string) string {
		rows, _ := q.ListSettings(ctx)
		for _, r := range rows {
			if r.Key == k {
				return r.Value
			}
		}
		return ""
	}
	// legacy plaintext is still read, then migrated
	conn.Exec(`INSERT INTO settings (key, value) VALUES ('smtp_pass', 'legacy'), ('company_name', 'ACME')`)
	if get("smtp_pass") != "legacy" {
		t.Fatal("legacy plaintext not read")
	}
	for range 2 { // idempotent
		if err := EncryptSettings(ctx, conn); err != nil {
			t.Fatal(err)
		}
	}
	if v := raw("smtp_pass"); !strings.HasPrefix(v, "enc1:") || strings.Contains(v, "legacy") {
		t.Fatalf("not encrypted: %q", v)
	}
	if get("smtp_pass") != "legacy" || raw("company_name") != "ACME" {
		t.Fatal("migration changed the value or a non-secret key")
	}
	// round trip through UpsertSetting
	q.UpsertSetting(ctx, UpsertSettingParams{Key: "tripay_api_key", Value: "abc"})
	if raw("tripay_api_key") == "abc" || get("tripay_api_key") != "abc" {
		t.Fatalf("round trip failed: raw %q", raw("tripay_api_key"))
	}
	// wrong key reads empty, not garbage
	SettingsKey = []byte(strings.Repeat("x", 32))
	if get("tripay_api_key") != "" {
		t.Fatal("opened with the wrong key")
	}
}
