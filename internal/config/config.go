// Package config reads every GOBILL_* environment variable once, at startup. See
// docs/en/configuration.md and config.env.example for the operator view.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config holds the environment settings of one gobill process.
type Config struct {
	// DB is the SQLite file (GOBILL_DB, default "./gobill.db").
	DB string
	// HTTP is the HTTP listen address (GOBILL_HTTP, default ":8080").
	HTTP string
	// Secure sets the Secure flag on session cookies (GOBILL_HTTPS; anything but "0" means on).
	Secure bool
	// PProf is the Go profiler address (GOBILL_PPROF, default off). It has no login.
	PProf string
	// SecretKey replaces the <DB>.key file when set (GOBILL_SECRET_KEY).
	SecretKey string
	// AllowNewKey lets startup create a new key over sealed data (GOBILL_ALLOW_NEW_KEY=1).
	AllowNewKey bool
	// BackupDir is the backup folder (GOBILL_BACKUP_DIR). Empty means a "backup" folder next to
	// the database; use Backup to resolve it.
	BackupDir string
	// BackupMirror is a second folder for daily backups (GOBILL_BACKUP_MIRROR, default off).
	BackupMirror string
	// RadiusAddr is the RADIUS UDP auth address (GOBILL_RADIUS). Unset means ":1812"; empty or
	// "off" turns the listener off, and then RadiusOn is false.
	RadiusAddr string
	RadiusOn   bool
	// StaleAfter overrides radius.StaleAfter (GOBILL_STALE_AFTER, 1m to 1h). Zero means unset.
	StaleAfter time.Duration
	// NASOnlineWindow overrides web.NASFreshFor (GOBILL_NAS_ONLINE_WINDOW, 1m to 24h). Zero means unset.
	NASOnlineWindow time.Duration
	// Jobs starts the background jobs (GOBILL_JOBS; anything but "0" means on).
	Jobs bool
	// GOBILL_WG=off is still read by wg.Disabled, because internal/wg is out of scope for M7.
}

// Load reads the environment. A bad duration returns an error together with a Config that has
// everything else filled in, so subcommands that do not use the durations can ignore it.
func Load() (Config, error) {
	c := Config{
		DB:           env("GOBILL_DB", "./gobill.db"),
		HTTP:         env("GOBILL_HTTP", ":8080"),
		Secure:       os.Getenv("GOBILL_HTTPS") != "0",
		PProf:        os.Getenv("GOBILL_PPROF"),
		SecretKey:    os.Getenv("GOBILL_SECRET_KEY"),
		AllowNewKey:  os.Getenv("GOBILL_ALLOW_NEW_KEY") == "1",
		BackupDir:    os.Getenv("GOBILL_BACKUP_DIR"),
		BackupMirror: os.Getenv("GOBILL_BACKUP_MIRROR"),
		Jobs:         os.Getenv("GOBILL_JOBS") != "0",
	}
	c.RadiusAddr, c.RadiusOn = ":1812", true
	if v, ok := os.LookupEnv("GOBILL_RADIUS"); ok {
		c.RadiusAddr, c.RadiusOn = v, v != "" && v != "off"
		if !c.RadiusOn {
			c.RadiusAddr = ""
		}
	}
	var err1, err2 error
	c.StaleAfter, err1 = envDuration("GOBILL_STALE_AFTER", time.Minute, time.Hour)
	c.NASOnlineWindow, err2 = envDuration("GOBILL_NAS_ONLINE_WINDOW", time.Minute, 24*time.Hour)
	if err1 != nil {
		return c, err1
	}
	return c, err2
}

// Backup returns the backup folder for the database at dbPath.
func (c Config) Backup(dbPath string) string {
	if c.BackupDir != "" {
		return c.BackupDir
	}
	return filepath.Join(filepath.Dir(dbPath), "backup")
}

// envDuration reads a Go duration ("10m", "90s") from key; 0 when the key is unset.
func envDuration(key string, lo, hi time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(v); err == nil && d >= lo && d <= hi {
		return d, nil
	}
	return 0, fmt.Errorf("%s=%q: want a duration between %s and %s, e.g. 10m", key, v, lo, hi)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
