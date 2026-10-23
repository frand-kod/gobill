package db

import (
	"context"
	"path/filepath"
	"testing"
)

// PinRouterCert only fills an empty pin, so the first of two racing first connects wins.
func TestPinRouterCertOnlyIfEmpty(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	ctx, q := context.Background(), New(conn)
	r, err := q.CreateRouter(ctx, CreateRouterParams{Name: "r", Host: "h", Port: 8729, Username: "u", PasswordEnc: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	q.PinRouterCert(ctx, PinRouterCertParams{CertPin: "aa", ID: r.ID})
	q.PinRouterCert(ctx, PinRouterCertParams{CertPin: "bb", ID: r.ID})
	if got, _ := q.GetRouter(ctx, r.ID); got.CertPin != "aa" {
		t.Fatalf("pin = %q, want aa", got.CertPin)
	}
}
