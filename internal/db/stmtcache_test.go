package db

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestStmtCacheReusesAndBounds(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := NewCached(conn)
	c := q.db.(*stmtCache)
	if NewCached(conn).db != c {
		t.Fatal("second NewCached on the same DB must share the cache")
	}
	ctx := t.Context()
	for range 3 {
		var n int
		if err := c.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	if len(c.m) != 1 {
		t.Fatalf("cached %d statements, want 1", len(c.m))
	}
	// Full cache: new texts still run, unprepared.
	for i := len(c.m); i < maxStmts; i++ {
		c.m[fmt.Sprint("fake", i)] = nil
	}
	var n int
	if err := c.QueryRowContext(ctx, "SELECT 2").Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if _, err := c.ExecContext(ctx, "CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	// A bad query reports its error through Scan.
	if err := c.QueryRowContext(ctx, "SELECT nope FROM nowhere").Scan(&n); err == nil {
		t.Fatal("want an error for a bad query")
	}
}
