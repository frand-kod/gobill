// Package restore holds the database side of backup and restore: checking an uploaded backup read-only,
// counting rows, the key fingerprint and the consistent copy made with VACUUM INTO. No HTTP here.
package restore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

// Counts are the rows gobill holds, which an import or restore replaces.
type Counts struct{ Customers, Plans, Subscriptions, Transactions, Admins int64 }

// Filled reports whether there is any data.
func (c Counts) Filled() bool {
	return c.Customers+c.Plans+c.Subscriptions+c.Transactions+c.Admins > 0
}

// CountRows counts the main tables of conn.
func CountRows(ctx context.Context, conn *sql.DB) (Counts, error) {
	var c Counts
	for _, t := range []struct {
		table string
		n     *int64
	}{{"customers", &c.Customers}, {"plans", &c.Plans}, {"subscriptions", &c.Subscriptions}, {"transactions", &c.Transactions}, {"admins", &c.Admins}} {
		if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM "+t.table).Scan(t.n); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Refusal is a file the restore turns down. Msg is an untranslated key, Detail is shown as is.
type Refusal struct{ Msg, Detail string }

func (e *Refusal) Error() string { return e.Msg }

func notBackup(err error) error {
	return &Refusal{Msg: "Not a gobill backup: the file is not a readable SQLite database.", Detail: err.Error()}
}

// KeyFingerprint is the first 8 hex characters of the SHA-256 of the key.
func KeyFingerprint(key []byte) string {
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:4])
}

func keyRefusal(key []byte, bakFP string) *Refusal {
	d := "key fingerprint of this app: " + KeyFingerprint(key)
	if bakFP != "" {
		d += ", key fingerprint of this backup: " + bakFP
	}
	return &Refusal{Msg: "Different encryption key: this backup was made with another GOBILL_SECRET_KEY", Detail: d}
}

// Inspect opens the file read-only and checks that gobill can restore it. It returns a *Refusal for a
// file it turns down. warn holds "<app fp> <backup fp>" when the key differs but nothing is sealed.
func Inspect(ctx context.Context, path string, key []byte, loc *time.Location) (counts Counts, newest, warn string, err error) {
	ro, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return Counts{}, "", "", notBackup(err)
	}
	defer ro.Close()
	var check string
	if err := ro.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return Counts{}, "", "", notBackup(err)
	}
	if check != "ok" {
		return Counts{}, "", "", notBackup(errors.New(check))
	}
	var ver int
	if err := ro.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return Counts{}, "", "", notBackup(err)
	}
	if ver == 0 {
		return Counts{}, "", "", notBackup(errors.New("no migrations applied"))
	}
	latest, err := db.LatestVersion()
	if err != nil {
		return Counts{}, "", "", err
	}
	if ver > latest {
		return Counts{}, "", "", &Refusal{Msg: "Backup from a newer gobill version", Detail: fmt.Sprintf("backup schema %d, this binary %d", ver, latest)}
	}
	counts, err = CountRows(ctx, ro)
	if err != nil {
		return Counts{}, "", "", notBackup(err)
	}
	// the backup carries the fingerprint of the key it was made with (settings key_fp); older backups have none
	var bakFP string
	_ = ro.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'key_fp'").Scan(&bakFP)
	// the sealed columns are encrypted with GOBILL_SECRET_KEY: one non-empty value tells whether the key matches
	for _, q := range []string{
		"SELECT password_enc FROM routers WHERE length(password_enc) > 0 LIMIT 1",
		"SELECT secret_enc FROM nas WHERE length(secret_enc) > 0 LIMIT 1",
		"SELECT secret_enc FROM customers WHERE length(secret_enc) > 0 LIMIT 1",
	} {
		var sealed []byte
		err := ro.QueryRowContext(ctx, q).Scan(&sealed)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Counts{}, "", "", notBackup(err)
		}
		if _, err := secret.Open(key, sealed); err != nil {
			return Counts{}, "", "", keyRefusal(key, bakFP)
		}
		break
	}
	// secret settings carry the same key; an empty result for a non-empty value means it did not open
	if srows, err := ro.QueryContext(ctx, "SELECT value FROM settings WHERE value LIKE 'enc1:%'"); err == nil {
		defer srows.Close()
		for srows.Next() {
			var v string
			if srows.Scan(&v) == nil && db.OpenSettingWith(key, v) == "" {
				return Counts{}, "", "", keyRefusal(key, bakFP)
			}
		}
	}
	// a different fingerprint with nothing sealed in the backup is allowed, with a warning
	if bakFP != "" && bakFP != KeyFingerprint(key) {
		warn = KeyFingerprint(key) + " " + bakFP
	}
	var last int64
	if err := ro.QueryRowContext(ctx, "SELECT coalesce(max(created_at), 0) FROM transactions").Scan(&last); err != nil {
		return Counts{}, "", "", notBackup(err)
	}
	if last == 0 {
		return counts, "", warn, nil
	}
	return counts, time.Unix(last, 0).In(loc).Format("2006-01-02 15:04"), warn, nil
}

// Backup writes a consistent copy of the database made with VACUUM INTO, next to the database file
// (not os.TempDir: that is often a RAM tmpfs too small for the copy). The caller calls cleanup when done.
func Backup(ctx context.Context, conn *sql.DB, dbPath string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(filepath.Dir(dbPath), "gobill-backup-")
	if err != nil {
		return "", nil, fmt.Errorf("backup dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(dir) }
	path = filepath.Join(dir, "backup.db")
	if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("vacuum into: %w", err)
	}
	return path, cleanup, nil
}
