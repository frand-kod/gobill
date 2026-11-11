// Package settings reads the settings table. Get and List read straight from the database through any
// Queries, so they also work inside a transaction and see its own writes. Store caches the whole table
// for readers that run per request.
package settings

import (
	"context"
	"maps"
	"sync"

	"github.com/frand-kod/gobill/internal/db"
)

// Get returns one setting, secret ones decrypted. A missing key or a read error gives "".
func Get(ctx context.Context, q *db.Queries, key string) string {
	v, err := q.GetSetting(ctx, key)
	if err != nil {
		return ""
	}
	if db.SecretSettings[key] {
		return db.OpenSetting(v)
	}
	return v
}

// List returns all settings, secret ones decrypted.
func List(ctx context.Context, q *db.Queries) (map[string]string, error) {
	rows, err := q.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m, nil
}

// Store is a cache of the settings table. It reloads when a setting was written through
// db.Queries.UpsertSetting (db.SettingsGen moved) or after Invalidate, so a reader never sees a value
// older than the last save. A write made in a transaction only counts at commit time through
// Invalidate, which the settings save handlers call after they commit.
type Store struct {
	q   *db.Queries
	mu  sync.Mutex
	gen uint64
	m   map[string]string
}

// New returns a Store that reads through q, which must not be a transaction.
func New(q *db.Queries) *Store { return &Store{q: q} }

// Invalidate drops the cache; the next read loads the table again.
func (s *Store) Invalidate() {
	s.mu.Lock()
	s.m = nil
	s.mu.Unlock()
}

// All returns a copy of all settings, secret ones decrypted. The caller may change it.
func (s *Store) All(ctx context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	return maps.Clone(s.m), nil
}

// View is All without the copy, for hot paths. The map is shared: the caller must not change it.
func (s *Store) View(ctx context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	return s.m, nil
}

// Get returns one setting, or "" when it is unset or the table cannot be read.
func (s *Store) Get(ctx context.Context, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.load(ctx) != nil {
		return ""
	}
	return s.m[key]
}

// load refreshes the cache when it is empty or a setting was saved. Caller holds mu.
func (s *Store) load(ctx context.Context) error {
	g := db.SettingsGen.Load()
	if s.m != nil && g == s.gen {
		return nil
	}
	m, err := List(ctx, s.q)
	if err != nil {
		return err
	}
	s.m, s.gen = m, g
	return nil
}
