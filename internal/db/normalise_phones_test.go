package db

import (
	"path/filepath"
	"testing"
)

// A row saved as typed is rewritten to the canonical form once; a second run changes nothing.
func TestNormalisePhonesOnce(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := New(conn)
	ctx := t.Context()
	for _, c := range []CreateCustomerParams{
		{Username: "budi", PasswordHash: "h", Fullname: "Budi", Phone: "0812-3456-789", ServiceType: "Others", Status: "Active"},
		{Username: "siti", PasswordHash: "h", Fullname: "Siti", Phone: "628123456780", ServiceType: "Others", Status: "Active"},
		{Username: "nophone", PasswordHash: "h", Fullname: "No", ServiceType: "Others", Status: "Active"},
	} {
		if _, err := q.CreateCustomer(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := NormalisePhones(ctx, conn); err != nil || n != 1 {
		t.Fatalf("first run: changed %d, err %v; want 1", n, err)
	}
	if c, _ := q.GetCustomerByUsername(ctx, "budi"); c.Phone != "628123456789" {
		t.Fatalf("budi phone %q, want 628123456789", c.Phone)
	}
	if n, err := NormalisePhones(ctx, conn); err != nil || n != 0 {
		t.Fatalf("second run: changed %d, err %v; want 0", n, err)
	}
}
