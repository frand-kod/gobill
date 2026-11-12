package promo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestValidateSaveMove(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: conn, Q: db.New(conn), Loc: time.UTC, Now: time.Now}
	ctx := context.Background()
	e := map[string]string{}
	s.Validate(Form{"title": "", "link": "javascript:x", "starts": "2026-10-10", "ends": "2026-10-01"}, e)
	if e["title"] == "" || e["link"] == "" || e["ends"] == "" {
		t.Fatalf("errs: %v", e)
	}
	for _, n := range []string{"a", "b"} {
		e = map[string]string{}
		p := s.Validate(Form{"title": n, "active": "1"}, e)
		if len(e) > 0 {
			t.Fatal(e)
		}
		if err := s.Save(ctx, 0, p, ""); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := s.Q.ListPromos(ctx)
	if err := s.Move(ctx, rows[1].ID, -1); err != nil {
		t.Fatal(err)
	}
	if rows, _ = s.Q.ListPromos(ctx); rows[0].Title != "b" {
		t.Fatalf("order: %v", rows)
	}
}
