package job

import (
	"context"
	"database/sql"
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
type Backup struct {
	Conn    *sql.DB
	Q       *db.Queries
	Dir     string
	Trusted func() bool
	Now     func() time.Time // injectable for tests; nil = time.Now
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
	return b.rotate(ctx)
}

// rotate deletes the oldest nuxbill-YYYYMMDD.db files beyond backup_keep (default 7).
func (b *Backup) rotate(ctx context.Context) error {
	keep := 7
	settings, err := b.Q.ListSettings(ctx)
	if err != nil {
		return err
	}
	for _, s := range settings {
		if s.Key == "backup_keep" {
			if n, err := strconv.Atoi(s.Value); err == nil && n >= 1 {
				keep = n
			}
		}
	}
	entries, err := os.ReadDir(b.Dir)
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
		if err := os.Remove(filepath.Join(b.Dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}
