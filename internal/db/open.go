package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens the SQLite file with the pragmas the app relies on.
// synchronous=FULL keeps committed billing data safe on sudden power loss (STB).
func Open(path string) (*sql.DB, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(FULL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=journal_size_limit(16777216)" +
		"&_pragma=temp_store(MEMORY)" +
		"&_pragma=cache_size(-8000)" +
		"&_txlock=immediate"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, err
	}
	// Planner stats for tables that have none or grew a lot, e.g. right after an install or an import.
	// Without them RADIUS auth picked the status index and scanned every active subscription
	// (about 30x slower at 5000 customers) until the daily cleanup ran PRAGMA optimize.
	// A failure here only costs speed, so it does not stop the start.
	conn.Exec("PRAGMA optimize=0x10002")
	conn.SetMaxOpenConns(4) // ponytail: fixed pool for the STB's RAM; raise if portal requests queue
	// Keep them all open: the default 2 idle conns made concurrent requests reopen a connection
	// (and rerun every pragma) for most queries, about a quarter of the CPU under load.
	conn.SetMaxIdleConns(4)
	return conn, nil
}

// Migrate applies every migrations/NNNN_*.sql newer than PRAGMA user_version,
// each in its own transaction.
func Migrate(conn *sql.DB) error {
	var current int
	if err := conn.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return err
	}

	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		base := strings.TrimPrefix(name, "migrations/")
		version, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", base)
		}
		if version <= current {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", base, err)
		}
		// PRAGMA does not accept bound parameters; version is a parsed int.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		current = version
	}
	return nil
}
