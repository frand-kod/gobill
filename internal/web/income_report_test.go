package web

import (
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// S1 + money#5: reports use the dashboard income: purchases paid from balance (the top-up already counted)
// and gifts are not income.
func TestReportSkipsBalanceMovements(t *testing.T) {
	s, q := newTestApp(t)
	for i, r := range []struct{ method, typ string }{
		{"Cash - A", "PPPoE"}, {"Customer - Balance", "PPPoE"}, {"Customer - Balance", "Balance"}, {"Balance - Gift from x", "Balance"},
	} {
		if _, err := s.conn.Exec(`INSERT INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
			VALUES (?, 'u', 'p', 'r', ?, 100, ?, 1000, 0, 0)`, "INV-"+itoa(int64(i+1)), r.typ, r.method); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := q.ReportTransactions(t.Context(), db.ReportTransactionsParams{FromTs: 0, ToTs: 2000})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %d %v", len(rows), err)
	}
}
