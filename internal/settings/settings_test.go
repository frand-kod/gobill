package settings

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func testQ(t *testing.T) (*db.Queries, func(string)) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return db.New(conn), func(sql string) {
		if _, err := conn.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreSeesSavesAndRawInsertNeedsInvalidate(t *testing.T) {
	ctx := context.Background()
	q, exec := testQ(t)
	st := New(q)
	if st.Get(ctx, "x") != "" {
		t.Fatal("unset key must read empty")
	}
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "x", Value: "1"}); err != nil {
		t.Fatal(err)
	}
	if st.Get(ctx, "x") != "1" {
		t.Fatal("save through UpsertSetting not seen")
	}
	exec(`UPDATE settings SET value = '2' WHERE key = 'x'`) // bypasses UpsertSetting, like a transaction
	if st.Get(ctx, "x") != "1" {
		t.Fatal("expected the cached value")
	}
	st.Invalidate()
	if st.Get(ctx, "x") != "2" {
		t.Fatal("Invalidate did not reload")
	}
}

func TestAllIsACopy(t *testing.T) {
	ctx := context.Background()
	q, _ := testQ(t)
	st := New(q)
	_ = q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "a", Value: "1"})
	m, _ := st.All(ctx)
	m["a"] = "changed"
	if st.Get(ctx, "a") != "1" {
		t.Fatal("changing the returned map changed the cache")
	}
}

func TestSecretsDecrypted(t *testing.T) {
	ctx := context.Background()
	q, _ := testQ(t)
	db.SettingsKey = []byte(strings.Repeat("k", 32))
	t.Cleanup(func() { db.SettingsKey = nil })
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "smtp_pass", Value: "pw"}); err != nil {
		t.Fatal(err)
	}
	if Get(ctx, q, "smtp_pass") != "pw" || New(q).Get(ctx, "smtp_pass") != "pw" {
		t.Fatal("secret not decrypted")
	}
	m, _ := List(ctx, q)
	if m["smtp_pass"] != "pw" {
		t.Fatal("List did not decrypt")
	}
}

func TestValidate(t *testing.T) {
	langs := []string{"indonesia", "english"}
	bad := map[string]string{
		"company_name": "", "isolir_days": "4000", "isolir_allow_hosts": "wa.me\nfoo bar", "isolir_rate_down": "fast",
		"language": "klingon", "timezone": "Mars/Base", "reminder_hour": "24", "customer_password_min": "3",
		"wg_port": "0", "wg_subnet": "8.8.8.0/24", "trusted_proxies": "0.0.0.0/0", "radius_rest_allow": "nope",
		"tripay_mode": "live", "webhook_url": "ftp://x",
	}
	got := Validate(bad, langs)
	for k := range bad {
		if got[k] == "" {
			t.Errorf("%s=%q accepted", k, bad[k])
		}
	}
	good := map[string]string{
		"company_name": "ACME", "isolir_days": "30", "isolir_allow_hosts": "wa.me\r\n\r\n*.whatsapp.net\r\n", "isolir_rate_down": "64k",
		"language": "english", "timezone": "Asia/Jakarta", "reminder_hour": "7", "customer_password_min": "4",
		"wg_port": "51820", "wg_subnet": "10.99.0.0/24", "trusted_proxies": "10.0.0.0/24, 127.0.0.1",
		"radius_rest_allow": "10.0.0.1, 192.168.0.0/16", "tripay_mode": "sandbox", "webhook_url": "https://x.test/h",
	}
	if e := Validate(good, langs); len(e) != 0 {
		t.Errorf("valid values rejected: %v", e)
	}
	if e := Validate(map[string]string{}, langs); len(e) != 0 {
		t.Errorf("empty input must give no errors: %v", e)
	}
}

func TestValidateRadiusRestSecretLength(t *testing.T) {
	if e := Validate(map[string]string{"radius_rest_secret": "short"}, nil); e["radius_rest_secret"] == "" {
		t.Fatal("short secret accepted")
	}
	for _, ok := range []string{"", "0123456789abcdef"} {
		if e := Validate(map[string]string{"radius_rest_secret": ok}, nil); e["radius_rest_secret"] != "" {
			t.Fatalf("%q refused: %v", ok, e)
		}
	}
}
