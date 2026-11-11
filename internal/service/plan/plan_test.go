package plan

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestSaveValidatesAndNotFound(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: conn, Q: db.New(conn)}
	ctx := context.Background()
	if _, err := s.Save(ctx, &SaveInput{ID: 999, V: map[string]string{}, Errs: map[string]string{}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	in := &SaveInput{V: map[string]string{"type": "Balance", "billing": "prepaid", "validity_unit": "Days", "price": "10000", "name": "Topup"}, Errs: map[string]string{}}
	if res, err := s.Save(ctx, in); err != nil || !res.Saved || res.Plan.Device != "" {
		t.Fatalf("balance plan: %+v %v %v", res, err, in.Errs)
	}
	in = &SaveInput{V: map[string]string{"type": "Hotspot", "billing": "prepaid", "validity_unit": "Days", "price": "x"}, Errs: map[string]string{}}
	if res, _ := s.Save(ctx, in); res.Saved || in.Errs["name"] == "" || in.Errs["price"] == "" || in.Errs["bandwidth_id"] == "" {
		t.Fatalf("errs: %v", in.Errs)
	}
}
