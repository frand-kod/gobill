package importer

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// TestFields imports PHP customer attributes with a SQLite stand-in for MySQL.
func TestFields(t *testing.T) {
	ctx := context.Background()
	my, _ := sql.Open("sqlite", ":memory:")
	my.SetMaxOpenConns(1)
	defer my.Close()
	if _, err := my.Exec(`CREATE TABLE tbl_customers_fields (id, customer_id, field_name, field_value);
		INSERT INTO tbl_customers_fields VALUES (1, 1, 'Router Bill', '5000:3'), (2, 1, 'Invoice', '100000'),
		(3, 1, 'Expired Date', '15'), (4, 99, 'Router Bill', '1'), (5, 1, 'Expired Date', 'x');`); err != nil {
		t.Fatal(err)
	}
	lite, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	if err := db.Migrate(lite); err != nil {
		t.Fatal(err)
	}
	tx, _ := lite.BeginTx(ctx, nil)
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO customers (id, username, password_hash, fullname, service_type, status, created_at) VALUES (1, 'budi', 'h', 'B', 'PPPoE', 'Active', 1)`); err != nil {
		t.Fatal(err)
	}
	m := &imp{my: my, tx: tx, ctx: ctx, rep: &Report{}, custIDs: map[int64]bool{1: true}}
	if err := m.fields(); err != nil {
		t.Fatal(err)
	}
	var v string
	tx.QueryRow(`SELECT v.value FROM customer_field_values v JOIN custom_fields f ON f.id = v.field_id WHERE f.name = 'Router Bill'`).Scan(&v)
	var day int
	tx.QueryRow(`SELECT billing_day FROM customers WHERE id = 1`).Scan(&day)
	if v != "5000:3" || day != 15 || len(m.rep.Tables[0].Skips) != 2 {
		t.Errorf("bill %q day %d skips %v", v, day, m.rep.Tables[0].Skips)
	}
}
