package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"
)

// Snapshot writes a VACUUM INTO copy of conn to dir, named gobill-YYYYMMDD-HHMMSS-<label>.db, and
// returns the file name. The name does not match the daily backup pattern, so the daily rotation
// never deletes it. Used before a PHPNuxBill import, a restore and a gobill update.
func Snapshot(ctx context.Context, conn *sql.DB, dir, label string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "gobill-" + time.Now().Format("20060102-150405") + "-" + label + ".db"
	final := filepath.Join(dir, name)
	tmp := final + ".tmp"
	os.Remove(tmp)
	if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}
