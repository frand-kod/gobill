package admin

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func TestLegacyLoginAndSave(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	s := &Service{Q: q, Cost: bcrypt.MinCost}
	ctx := context.Background()
	sum := sha1.Sum([]byte("oldpass"))
	a, err := q.CreateAdmin(ctx, db.CreateAdminParams{Username: "legacy", Fullname: "Legacy", PasswordHash: "x", Role: "Admin"})
	if err != nil {
		t.Fatal(err)
	}
	conn.ExecContext(ctx, "UPDATE admins SET legacy_sha1 = ? WHERE id = ?", hex.EncodeToString(sum[:]), a.ID)
	if s.LegacyLogin(ctx, a.ID, "wrong") || !s.LegacyLogin(ctx, a.ID, "oldpass") {
		t.Fatal("legacy login")
	}
	got, _ := q.GetAdmin(ctx, a.ID)
	if bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte("oldpass")) != nil || got.LegacySha1 == "" {
		t.Fatal("rehash must set bcrypt and keep the marker")
	}

	in := SaveInput{ActorID: a.ID, ActorRole: "Admin", Assignable: []string{"Report"},
		V: map[string]string{"username": "ab", "fullname": "Full Name", "role": "SuperAdmin"}}
	if _, err := s.Save(ctx, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
	in.V["role"] = "Report"
	res, err := s.Save(ctx, in)
	if err != nil || res.Errs["username"] == "" || res.Errs["password"] == "" {
		t.Fatalf("errs: %+v %v", res, err)
	}
	in.V["username"], in.Pass, in.CPass = "newuser", "longenough1", "longenough1"
	if res, err = s.Save(ctx, in); err != nil || len(res.Errs) > 0 || res.Status != "Active" {
		t.Fatalf("create: %+v %v", res, err)
	}
}
