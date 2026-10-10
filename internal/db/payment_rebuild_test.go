package db

import (
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

// Migration 0013 rebuilds payment_requests on a populated DB: duplicate gateway_refs must not fail it,
// username is filled from the customer, and deleting the customer keeps the row with customer_id NULL.
func TestPaymentRequestsRebuildKeepsRows(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(names)
	for _, name := range names {
		if filepath.Base(name) >= "0012" {
			break
		}
		body, _ := migrations.ReadFile(name)
		if _, err := conn.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(`PRAGMA user_version = 11;
		INSERT INTO customers (id, username, password_hash, fullname) VALUES (1, 'alice', 'x', 'Alice'), (2, 'bob', 'x', 'Bob');
		INSERT INTO payment_requests (id, ref, gateway, gateway_ref, customer_id, plan_id, amount, expires_at) VALUES
			(1, 'R1', 'tripay', 'G1', 1, 1, 1000, 0),
			(2, 'R2', 'tripay', 'G1', 1, 1, 1000, 0),
			(3, 'R3', 'tripay', 'G3', 2, 1, 1000, 0);`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	ref := func(r string) (gw, user string, cid any) {
		t.Helper()
		if err := conn.QueryRow("SELECT gateway_ref, username, customer_id FROM payment_requests WHERE ref = ?", r).Scan(&gw, &user, &cid); err != nil {
			t.Fatal(err)
		}
		return
	}
	if gw, user, _ := ref("R1"); gw != "G1" || user != "alice" {
		t.Fatalf("R1: gateway_ref %q username %q", gw, user)
	}
	if gw, user, _ := ref("R2"); gw != "" || user != "alice" {
		t.Fatalf("R2 duplicate: gateway_ref %q username %q", gw, user)
	}
	if _, _, cid := ref("R3"); cid != int64(2) {
		t.Fatalf("R3 customer_id %v", cid)
	}
	if _, err := conn.Exec("DELETE FROM customers WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, user, cid := ref("R1"); cid != nil || user != "alice" {
		t.Fatalf("after customer delete: customer_id %v username %q", cid, user)
	}
}
