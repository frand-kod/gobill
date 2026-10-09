package web

import (
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// S1: reports leave out the gobill-only transfer / send-plan rows (PHP never writes them) but
// keep balance-paid purchases, as PHP reports do.
func TestReportSkipsBalanceMovements(t *testing.T) {
	s, q := newTestApp(t)
	for i, r := range []struct{ method, typ string }{
		{"Cash - A", "PPPoE"}, {"Customer - Balance", "PPPoE"}, {"Customer - Balance", "Balance"},
	} {
		if _, err := s.conn.Exec(`INSERT INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
			VALUES (?, 'u', 'p', 'r', ?, 100, ?, 1000, 0, 0)`, "INV-"+itoa(int64(i+1)), r.typ, r.method); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := q.ReportTransactions(t.Context(), db.ReportTransactionsParams{FromTs: 0, ToTs: 2000})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %d %v", len(rows), err)
	}
}
