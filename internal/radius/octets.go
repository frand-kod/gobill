package radius

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/frand-kod/gobill/internal/db"
)

// octCacheMax caps the cached sessions. Sessions closed without a Stop (NAS reboot, stale) are never
// evicted one by one, so the whole cache is dropped at the cap and refills from the database.
// ponytail: drop-all at the cap; an LRU if a NAS park ever holds more open sessions than this.
const octCacheMax = 50000

type octets struct {
	in, out int64
	gone    bool // pending only: the session stopped, forget it after commit
}

// octCache holds the last counters written for each open session, so an interim does not read
// radius_sessions before its upsert. Writes land in pending first and reach m only when their
// transaction commits; a failed commit drops those keys from m too. A miss reads the database, so
// any doubt falls back to the old behaviour.
type octCache struct {
	mu      sync.Mutex
	m       map[string]octets
	pending map[string]octets
}

func octKey(nas, sid string) string { return nas + "\x00" + sid }

// get returns the counters stored for the session: pending, then cached, then the database.
func (c *octCache) get(ctx context.Context, q *db.Queries, nas, sid string) (octets, error) {
	k := octKey(nas, sid)
	c.mu.Lock()
	o, ok := c.pending[k]
	if !ok {
		o, ok = c.m[k]
	}
	c.mu.Unlock()
	if ok {
		return o, nil
	}
	r, err := q.GetRadiusSessionOctets(ctx, db.GetRadiusSessionOctetsParams{NasIp: nas, SessionID: sid})
	if errors.Is(err, sql.ErrNoRows) {
		return octets{}, nil
	}
	return octets{in: r.InputOctets, out: r.OutputOctets}, err
}

// put records counters already written in the current transaction.
func (c *octCache) put(nas, sid string, o octets) {
	c.mu.Lock()
	if c.pending == nil {
		c.pending = map[string]octets{}
	}
	c.pending[octKey(nas, sid)] = o
	c.mu.Unlock()
}

// done ends a transaction: on success pending moves into the cache, on failure its keys are forgotten.
func (c *octCache) done(ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= octCacheMax {
		c.m = map[string]octets{}
	}
	for k, o := range c.pending {
		if !ok || o.gone {
			delete(c.m, k)
		} else {
			c.m[k] = o
		}
	}
	clear(c.pending)
}
