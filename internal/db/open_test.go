package db

import (
	"path/filepath"
	"sync"
	"testing"
)

// Two read-then-write transactions racing must both commit (_txlock=immediate), not fail with SQLITE_BUSY.
func TestConcurrentReadThenWrite(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE TABLE c (n INTEGER); INSERT INTO c VALUES (0)"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := conn.Begin()
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback()
			var n int
			if err := tx.QueryRow("SELECT n FROM c").Scan(&n); err != nil {
				errs <- err
				return
			}
			if _, err := tx.Exec("UPDATE c SET n = ?", n+1); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit()
		}()
	}
	wg.Wait()
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	conn.QueryRow("SELECT n FROM c").Scan(&n)
	if n != 8 {
		t.Fatalf("n = %d, want 8", n)
	}
}

// Optimize must leave no STAT4 samples: with them SQLite recompiles every indexed lookup on each run.
func TestOptimizeDropsStat4(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE TABLE c (n TEXT UNIQUE); WITH RECURSIVE i(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM i WHERE x < 500) INSERT INTO c SELECT 'u'||x FROM i"); err != nil {
		t.Fatal(err)
	}
	if err := Optimize(t.Context(), conn, "ANALYZE"); err != nil {
		t.Fatal(err)
	}
	var stat1, stat4 int
	conn.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_stat1'").Scan(&stat1)
	conn.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_stat4'").Scan(&stat4)
	if stat1 != 1 || stat4 != 0 {
		t.Fatalf("sqlite_stat1 tables %d (want 1), sqlite_stat4 tables %d (want 0)", stat1, stat4)
	}
}
