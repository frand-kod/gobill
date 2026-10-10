package db

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// LatestVersion is the highest migration number embedded in this binary.
func LatestVersion() (int, error) {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return 0, err
	}
	latest := 0
	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		v, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return 0, err
		}
		latest = max(latest, v)
	}
	return latest, nil
}

// ApplyPendingRestore swaps in a backup staged as path+".restore" by the Restore database page.
// Call it before Open. The live database and its -wal move aside next to it as <path>.pre-restore-<ts>
// (the same directory, so the rename never crosses filesystems). The -wal may hold committed data not
// yet checkpointed. The -shm is deleted. A stale -wal left beside the restored file would be replayed
// onto it. Then the staged file takes the name path.
func ApplyPendingRestore(path string) error {
	staged := path + ".restore"
	if _, err := os.Stat(staged); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	old := path + ".pre-restore-" + time.Now().Format("20060102-150405")
	if err := os.Remove(path + "-shm"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, p := range []struct{ from, to string }{{path, old}, {path + "-wal", old + "-wal"}} {
		if err := os.Rename(p.from, p.to); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	slog.Info("restore: current database moved aside", "db", old, "wal", old+"-wal")
	if err := os.Rename(staged, path); err != nil {
		return err
	}
	slog.Info("restore: backup applied", "db", path, "from", staged)
	return nil
}
