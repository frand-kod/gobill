package job

import (
	"context"
	"database/sql"
	"fmt"
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
// failure is logged and alerted, and never fails the local backup.
type Backup struct {
	Conn    *sql.DB
	Q       *db.Queries
	Dir     string
	Mirror  string // optional; must already exist, so an unmounted disk fails instead of creating a folder on the SD card
	Trusted func() bool
	Alert   func(ctx context.Context, msg string) error // operator alert (Telegram); nil = log only
	Now     func() time.Time                            // injectable for tests; nil = time.Now
}

// Run is the job.Run body. It ticks every minute; it writes only during the backup hour and
// only when today's file is missing, so repeated ticks in that hour are harmless.
func (b *Backup) Run(ctx context.Context) error {
	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}
	if now.Hour() != backupHour {
		return nil
	}
	final := filepath.Join(b.Dir, "nuxbill-"+now.Format("20060102")+".db")
	if _, err := os.Stat(final); err == nil {
		return nil // never overwrite today's file
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

// mirror copies the new backup to Mirror, prunes it to backup_keep, and records the result in settings.
// Attempts happen once a day, so an alert is sent at most once a day while it keeps failing,
// and once more when the failure clears.
func (b *Backup) mirror(ctx context.Context, src string, now time.Time) {
	st, err := b.settings(ctx)
	if err != nil {
		slog.Error("backup mirror", "err", err)
		return
	}
	dst := filepath.Join(b.Mirror, filepath.Base(src))
	err = copyFile(src, dst)
	if err == nil {
		err = prune(b.Mirror, keepN(st))
	}
	if err != nil {
		slog.Error("backup mirror failed", "dir", b.Mirror, "err", err)
		b.set(ctx, "backup_mirror_error", err.Error())
		b.alert(ctx, fmt.Sprintf("Backup mirror %s failed: %v", b.Mirror, err))
		return
	}
	slog.Info("backup mirrored", "path", dst)
	b.set(ctx, "backup_mirror_at", now.Format("2006-01-02 15:04:05"))
	if st["backup_mirror_error"] != "" {
		b.set(ctx, "backup_mirror_error", "")
		b.alert(ctx, fmt.Sprintf("Backup mirror %s is working again", b.Mirror))
	}
}

// alert sends the operator alert, if one is wired. Failures are logged only.
func (b *Backup) alert(ctx context.Context, msg string) {
	if b.Alert == nil {
		return
	}
	if err := b.Alert(ctx, msg); err != nil {
		slog.Error("backup alert", "err", err)
	}
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
