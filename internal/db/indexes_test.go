package db

import (
	"path/filepath"
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
