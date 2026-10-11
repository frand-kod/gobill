package job

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestUpdateCheckParsesReleaseOnSchedule(t *testing.T) {
	ctx := t.Context()
	conn, err := db.Open(filepath.Join(t.TempDir(), "gobill.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"https://example.test/releases/v0.2.0","body":"x"}`))
	}))
	defer srv.Close()

	start := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	now := start
	c := &UpdateCheck{Q: db.New(conn), URL: srv.URL, Client: srv.Client(), Now: func() time.Time { return now }}

	if err := c.Run(ctx); err != nil || hits.Load() != 0 {
		t.Fatalf("first tick checked too early: %d requests, %v", hits.Load(), err)
	}
	now = start.Add(4 * time.Minute)
	if err := c.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if tag, link := c.Latest(); tag != "v0.2.0" || link != "https://example.test/releases/v0.2.0" {
		t.Fatalf("Latest() = %q, %q", tag, link)
	}
	now = start.Add(5 * time.Hour)
	c.Run(ctx)
	if hits.Load() != 1 {
		t.Fatalf("checked again within 24 h: %d requests", hits.Load())
	}
}

func TestUpdateCheckOffDropsAnswer(t *testing.T) {
	ctx := t.Context()
	conn, err := db.Open(filepath.Join(t.TempDir(), "gobill.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"u"}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	c := &UpdateCheck{Q: q, URL: srv.URL, Client: srv.Client(), Now: func() time.Time { return now }}
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "update_check", Value: "no"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	c.Run(ctx)
	if hits.Load() != 0 {
		t.Fatalf("checked while update_check=no: %d requests", hits.Load())
	}
	if tag, _ := c.Latest(); tag != "" {
		t.Fatalf("Latest() = %q while disabled", tag)
	}
}
