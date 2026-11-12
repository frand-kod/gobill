package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"GOBILL_DB", "GOBILL_HTTP", "GOBILL_HTTPS", "GOBILL_RADIUS", "GOBILL_JOBS",
		"GOBILL_BACKUP_DIR", "GOBILL_STALE_AFTER", "GOBILL_NAS_ONLINE_WINDOW", "GOBILL_ALLOW_NEW_KEY"} {
		t.Setenv(k, "x")
		os.Unsetenv(k)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DB != "./gobill.db" || c.HTTP != ":8080" || !c.Secure || !c.Jobs || c.AllowNewKey ||
		c.RadiusAddr != ":1812" || !c.RadiusOn || c.StaleAfter != 0 || c.Backup(c.DB) != "backup" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadOverride(t *testing.T) {
	t.Setenv("GOBILL_DB", "/d/g.db")
	t.Setenv("GOBILL_HTTPS", "0")
	t.Setenv("GOBILL_JOBS", "0")
	t.Setenv("GOBILL_RADIUS", "off")
	t.Setenv("GOBILL_STALE_AFTER", "20m")
	t.Setenv("GOBILL_BACKUP_DIR", "/b")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DB != "/d/g.db" || c.Secure || c.Jobs || c.RadiusOn || c.RadiusAddr != "" ||
		c.StaleAfter != 20*time.Minute || c.Backup(c.DB) != "/b" {
		t.Fatalf("override: %+v", c)
	}
	t.Setenv("GOBILL_RADIUS", ":11812")
	if c, _ = Load(); c.RadiusAddr != ":11812" || !c.RadiusOn {
		t.Fatalf("radius: %+v", c)
	}
}

func TestEnvDuration(t *testing.T) {
	for _, c := range []struct {
		v    string
		bad  bool
		want time.Duration
	}{
		{"", false, 0},
		{"20m", false, 20 * time.Minute},
		{"30s", true, 0}, // below the minimum
		{"2h", true, 0},  // above the maximum
		{"600", true, 0}, // no unit
	} {
		t.Setenv("GOBILL_TEST_WINDOW", c.v)
		d, err := envDuration("GOBILL_TEST_WINDOW", time.Minute, time.Hour)
		if (err != nil) != c.bad || d != c.want {
			t.Errorf("%q: got %s, %v", c.v, d, err)
		}
	}
}
