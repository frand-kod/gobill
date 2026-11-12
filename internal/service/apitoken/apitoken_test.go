package apitoken

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func setup(t *testing.T) (*Service, int64, *sql.DB) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	a, err := q.CreateAdmin(context.Background(), db.CreateAdminParams{Username: "ops", PasswordHash: "x", Role: "Admin"})
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Q: q}, a.ID, conn
}

func TestLifecycle(t *testing.T) {
	s, aid, _ := setup(t)
	ctx := context.Background()
	plain, row, err := s.Create(ctx, aid, "ci", 0)
	if err != nil || !strings.HasPrefix(plain, "gbt_") || len(plain) != 4+43 {
		t.Fatalf("create: %q %v", plain, err)
	}
	if row.TokenHash == plain || row.TokenHash != hash(plain) {
		t.Fatal("plaintext must not be stored")
	}
	if _, a, err := s.Verify(ctx, plain); err != nil || a.ID != aid {
		t.Fatalf("verify: %v", err)
	}
	if _, _, err := s.Verify(ctx, plain+"x"); err != ErrInvalid {
		t.Fatalf("wrong token: %v", err)
	}
	if err := s.Revoke(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, row.ID); err == nil {
		t.Fatal("second revoke should fail")
	}
	if _, _, err := s.Verify(ctx, plain); err != ErrInvalid {
		t.Fatalf("revoked: %v", err)
	}
	if l, _ := s.List(ctx, aid); len(l) != 1 || !l[0].RevokedAt.Valid {
		t.Fatalf("list: %v", l)
	}
}

func TestExpiryAndInactiveAdmin(t *testing.T) {
	s, aid, conn := setup(t)
	ctx := context.Background()
	plain, _, _ := s.Create(ctx, aid, "short", time.Hour)
	s.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, _, err := s.Verify(ctx, plain); err != ErrInvalid {
		t.Fatalf("expired: %v", err)
	}
	s.Now = nil
	if _, _, err := s.Verify(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE admins SET status = 'Inactive'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Verify(ctx, plain); err != ErrInvalid {
		t.Fatalf("inactive admin: %v", err)
	}
}
