package db

import (
	"context"
	"database/sql"
	"sync"
)

// stmtCache is a DBTX that prepares each query text once and reuses the statement.
// Without it SQLite parses the SQL on every call, which was about half the CPU of a
// RADIUS request. database/sql prepares the statement again on each pool connection
// as needed. Transactions (WithTx) use the *sql.Tx directly and are not cached.
type stmtCache struct {
	db *sql.DB
	mu sync.Mutex
	m  map[string]*sql.Stmt
}

var caches sync.Map // *sql.DB -> *stmtCache, so every Queries on one DB shares the statements

// NewCached is New with prepared statement reuse. Use it for long-lived Queries on the app DB.
func NewCached(conn *sql.DB) *Queries {
	c, _ := caches.LoadOrStore(conn, &stmtCache{db: conn, m: map[string]*sql.Stmt{}})
	return New(c.(*stmtCache))
}

// maxStmts bounds the cache: sqlc.slice queries get a new text for every list length.
const maxStmts = 1000

// stmt returns nil (and no error) when the cache is full; the caller then runs the query unprepared.
func (c *stmtCache) stmt(ctx context.Context, query string) (*sql.Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st, ok := c.m[query]; ok {
		return st, nil
	}
	if len(c.m) >= maxStmts {
		return nil, nil
	}
	// ponytail: prepared under the lock; the first call of each query is rare, so contention is too
	st, err := c.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	c.m[query] = st
	return st, nil
}

func (c *stmtCache) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	st, err := c.stmt(ctx, query)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return c.db.ExecContext(ctx, query, args...)
	}
	return st.ExecContext(ctx, args...)
}

func (c *stmtCache) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return c.db.PrepareContext(ctx, query)
}

func (c *stmtCache) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	st, err := c.stmt(ctx, query)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return c.db.QueryContext(ctx, query, args...)
	}
	return st.QueryContext(ctx, args...)
}

func (c *stmtCache) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	st, err := c.stmt(ctx, query)
	if err != nil || st == nil {
		// *sql.Row cannot carry an error from outside; run unprepared so Scan reports the real one.
		return c.db.QueryRowContext(ctx, query, args...)
	}
	return st.QueryRowContext(ctx, args...)
}
