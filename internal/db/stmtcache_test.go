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

// WithTxCached runs the cached statements inside the transaction: writes and reads see the
// transaction, a rollback undoes them, a commit keeps them, and the statements stay cached.
func TestWithTxCached(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q, ctx := NewCached(conn), t.Context()
	set := func(commit bool, v string) {
		t.Helper()
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		tq := q.WithTxCached(tx)
		if _, ok := tq.db.(*txStmts); !ok {
			t.Fatal("not the cached transaction")
		}
		if err := tq.UpsertSetting(ctx, UpsertSettingParams{Key: "k", Value: v}); err != nil {
			t.Fatal(err)
		}
		if got, err := tq.GetSetting(ctx, "k"); err != nil || got != v {
			t.Fatalf("inside the transaction: %q %v", got, err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	set(true, "a")
	set(false, "b")
	if got, _ := q.GetSetting(ctx, "k"); got != "a" {
		t.Fatalf("after commit a, rollback b: %q", got)
	}
	set(true, "c")
	if got, _ := q.GetSetting(ctx, "k"); got != "c" {
		t.Fatalf("second commit: %q", got)
	}
	if n := len(q.db.(*stmtCache).m); n < 2 {
		t.Fatalf("%d statements cached", n)
	}
}
