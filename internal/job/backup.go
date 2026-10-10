package job

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// backupHour is the local hour (02:00) when the daily backup is taken.
const backupHour = 2

var backupName = regexp.MustCompile(`^nuxbill-\d{8}\.db$`)

// Backup takes one VACUUM INTO copy of the database per day and keeps the newest backup_keep files.
// With Mirror set, each new copy is also written there (USB, NFS/SMB or rclone mount). A mirror
// failure is recorded in settings (backup_mirror_error) and never fails the local backup; the
// alert job reports it.
type Backup struct {
	Conn    *sql.DB
	Q       *db.Queries
	Dir     string
	Mirror  string // optional; must already exist, so an unmounted disk fails instead of creating a folder on the SD card
	Trusted func() bool
	Now     func() time.Time // injectable for tests; nil = time.Now
}

// backupTime is the layout of the backup_* status settings.
const backupTime = "2006-01-02 15:04:05"

// Run is the job.Run body. It ticks every minute; it writes only during the backup hour and
// only when today's file is missing, so repeated ticks in that hour are harmless. While today's
// copy is missing from the mirror, the mirror copy is retried once an hour.
func (b *Backup) Run(ctx context.Context) error {
	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	final := filepath.Join(b.Dir, "nuxbill-"+now.Format("20060102")+".db")
	if _, err := os.Stat(final); err == nil {
		b.retryMirror(ctx, final, now) // never overwrite today's file
		return nil
	}
	if now.Hour() != backupHour {
		return nil
	}
	if !b.Trusted() {
		slog.Warn("backup skipped: system clock not trusted")
		return nil
	}
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return err
	}
	tmp := final + ".tmp"
	os.Remove(tmp)
	if _, err := b.Conn.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	slog.Info("backup written", "path", final)
	b.set(ctx, "backup_last_at", now.Format("2006-01-02 15:04:05"))
	b.set(ctx, "backup_last_file", filepath.Base(final))
	if b.Mirror != "" {
		b.mirror(ctx, final, now)
	}
	return b.rotate(ctx)
}

// mirror copies the new backup to Mirror, prunes it to backup_keep, and records the attempt and
// the result in settings. backup_mirror_error stays set until a copy succeeds; the alert job
// reports that state.
func (b *Backup) mirror(ctx context.Context, src string, now time.Time) {
	st, err := b.settings(ctx)
	if err != nil {
		slog.Error("backup mirror", "err", err)
		return
	}
	b.set(ctx, "backup_mirror_try_at", now.Format(backupTime))
	dst := filepath.Join(b.Mirror, filepath.Base(src))
	err = copyFile(src, dst)
	if err == nil {
		err = prune(b.Mirror, keepN(st))
	}
	if err != nil {
		slog.Error("backup mirror failed", "dir", b.Mirror, "err", err)
		b.set(ctx, "backup_mirror_error", err.Error())
		return
	}
	slog.Info("backup mirrored", "path", dst)
	b.set(ctx, "backup_mirror_at", now.Format(backupTime))
	b.set(ctx, "backup_mirror_error", "")
}

// retryMirror retries the mirror copy of today's backup once an hour while it keeps failing.
// It does nothing unless the last mirror attempt failed for this same file.
func (b *Backup) retryMirror(ctx context.Context, src string, now time.Time) {
	if b.Mirror == "" {
		return
	}
	st, err := b.settings(ctx)
	if err != nil || st["backup_mirror_error"] == "" || st["backup_last_file"] != filepath.Base(src) {
		return
	}
	if last, err := time.ParseInLocation(backupTime, st["backup_mirror_try_at"], now.Location()); err == nil && now.Sub(last) < time.Hour {
		return
	}
	b.mirror(ctx, src, now)
}

// rotate deletes the oldest nuxbill-YYYYMMDD.db files beyond backup_keep (default 7).
func (b *Backup) rotate(ctx context.Context) error {
	st, err := b.settings(ctx)
	if err != nil {
		return err
	}
	return prune(b.Dir, keepN(st))
}

// settings returns all settings as a map.
func (b *Backup) settings(ctx context.Context) (map[string]string, error) {
	rows, err := b.Q.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rows))
	for _, s := range rows {
		m[s.Key] = s.Value
	}
	return m, nil
}

// set stores a backup status value for the admin UI. A failure is logged, not returned.
func (b *Backup) set(ctx context.Context, key, value string) {
	if err := b.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: key, Value: value}); err != nil {
		slog.Error("backup status", "key", key, "err", err)
	}
}

// keepN is the backup_keep setting, default 7.
func keepN(st map[string]string) int {
	if n, err := strconv.Atoi(st["backup_keep"]); err == nil && n >= 1 {
		return n
	}
	return 7
}

// prune deletes the oldest backup files in dir beyond keep.
func prune(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && backupName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // YYYYMMDD sorts by date
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// copyFile writes src to dst (0600) through dst+".tmp" and fsyncs it, so a power cut never leaves a partial file under the final name.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
