package db

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The RADIUS login lookup matches username OR pppoe_username. Both sides must use an index, not scan customers.
func TestRadiusLookupUsesIndex(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query("EXPLAIN QUERY PLAN "+getCustomerForRadius, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
		if strings.HasPrefix(detail, "SCAN customers") {
			t.Fatalf("radius lookup scans customers: %q", plan)
		}
	}
	if !strings.Contains(strings.Join(plan, "|"), "SEARCH customers USING") {
		t.Fatalf("radius lookup does not search an index: %q", plan)
	}
}

func migrated(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func planOf(t *testing.T, conn *sql.DB, q string, args ...any) string {
	t.Helper()
	rows, err := conn.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		rows.Scan(&id, &parent, &notused, &detail)
		plan = append(plan, detail)
	}
	return strings.Join(plan, " | ")
}

// The one-statement RADIUS login must find the customer and the subscription through indexes.
func TestRadiusLoginUsesIndex(t *testing.T) {
	p := planOf(t, migrated(t), getRadiusLogin, "alice")
	if strings.Contains(p, "SCAN c") || strings.Contains(p, "SCAN s") || strings.Contains(p, "SCAN subscriptions") {
		t.Fatalf("GetRadiusLogin scans: %s", p)
	}
}

// Income sums are answered from transactions_income_idx alone; reports keep their sort-free order.
func TestIncomeSumCoveringIndex(t *testing.T) {
	conn := migrated(t)
	if p := planOf(t, conn, sumTransactionsBetween, 1, 2); !strings.Contains(p, "COVERING INDEX transactions_income_idx") {
		t.Fatalf("income sum: %s", p)
	}
	if p := planOf(t, conn, reportTransactions, 1, 2, "", "", "", 0); strings.Contains(p, "TEMP B-TREE") {
		t.Fatalf("report sorts: %s", p)
	}
}

// The default customer list shortcut returns exactly what FilterCustomers returns for that view.
func TestFilterCustomersNewestMatches(t *testing.T) {
	conn := migrated(t)
	if _, err := conn.Exec(`WITH RECURSIVE i(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM i WHERE x < 45)
		INSERT INTO customers (username, fullname, password_hash, service_type, status) SELECT 'u'||x, 'U '||x, 'h', 'Hotspot', 'Active' FROM i`); err != nil {
		t.Fatal(err)
	}
	q := New(conn)
	for _, pg := range []FilterCustomersNewestParams{{PageOffset: 0, PageLimit: 20}, {PageOffset: 40, PageLimit: 20}, {PageOffset: 0, PageLimit: -1}} {
		want, err := q.FilterCustomers(t.Context(), FilterCustomersParams{PageOffset: pg.PageOffset, PageLimit: pg.PageLimit})
		if err != nil {
			t.Fatal(err)
		}
		got, err := q.FilterCustomersNewest(t.Context(), pg)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%+v: %d rows, want %d", pg, len(got), len(want))
		}
		for i := range got {
			if !reflect.DeepEqual(FilterCustomersRow(got[i]), want[i]) {
				t.Fatalf("%+v row %d: %+v, want %+v", pg, i, got[i], want[i])
			}
		}
	}
}
