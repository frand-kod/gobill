package customer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestSaveClashAndCreate(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: conn, Q: db.New(conn), Key: make([]byte, 32), BcryptCost: 4}
	ctx := context.Background()
	adm, err := s.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: "a", Role: "SuperAdmin", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	form := func(user string) *SaveInput {
		return &SaveInput{V: map[string]string{"username": user, "fullname": "A", "service_type": "PPPoE", "status": "Active"},
			Pass: "secret123", AdminID: adm.ID, Errs: map[string]string{}}
	}
	in := form("budi")
	if res, err := s.Save(ctx, in); err != nil || !res.Saved || res.Created.ID == 0 {
		t.Fatalf("create: %+v %v %v", res, err, in.Errs)
	}
	in = form("budi")
	if res, err := s.Save(ctx, in); err != nil || res.Saved || in.Errs["username"] == "" {
		t.Fatalf("clash saved: %+v %v %v", res, err, in.Errs)
	}
	in = form("")
	if res, _ := s.Save(ctx, in); res.Saved || in.Errs["username"] != "This field is required" {
		t.Fatalf("empty username: %v", in.Errs)
	}
}
