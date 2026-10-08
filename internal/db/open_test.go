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
