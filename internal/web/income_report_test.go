package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// Agent deposits are their own income line; the sale of a consignment code is not counted twice.
func TestReportAgentDeposits(t *testing.T) {
	s, _ := newTestApp(t)
	ex := func(q string, a ...any) {
		t.Helper()
		if _, err := s.conn.Exec(q, a...); err != nil {
			t.Fatal(err)
		}
	}
	ex(`INSERT INTO plans (name, price, type, validity, validity_unit) VALUES ('P', 5000, 'Balance', 1, 'Days')`)
	ex(`INSERT INTO admins (username, password_hash, role) VALUES ('ag', 'x', 'Agent')`)
	ex(`INSERT INTO agent_batches (id, agent_id, plan_id, paid_count, bonus_count, unit_price) VALUES (1, (SELECT id FROM admins WHERE username='ag'), (SELECT id FROM plans WHERE name='P'), 2, 0, 4000)`)
	ex(`INSERT INTO vouchers (code, plan_id) SELECT 'AG1', id FROM plans WHERE name='P'`)
	ex(`INSERT INTO vouchers (code, plan_id) SELECT 'DIRECT', id FROM plans WHERE name='P'`)
	ex(`INSERT INTO agent_batch_vouchers (voucher_id, batch_id) SELECT id, 1 FROM vouchers WHERE code='AG1'`)
	ex(`INSERT INTO agent_ledger (batch_id, kind, amount, created_at) VALUES (1, 'debt', 8000, 1000), (1, 'payment', 3000, 1000), (1, 'payment', 1000, 1000)`)
	for i, m := range []string{"Voucher - AG1", "Voucher - DIRECT"} { // the agent's customer redeems; a walk-in redeems a direct code
		ex(`INSERT INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
			VALUES (?, 'u', 'P', 'r', 'Hotspot', 5000, ?, 1000, 0, 0)`, "INV-"+itoa(int64(i+1)), m)
	}
	get := func(url string) reportData {
		d, _, err := s.reportLoad(httptest.NewRequest("GET", url, nil), true)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	day := time.Unix(1000, 0).In(s.location()).Format("2006-01-02")
	d := get("/?from=" + day + "&to=" + day)
	// DIRECT 5000 + deposits 4000; AG1's 5000 is the agent's sale
	if d.Sum != 9000 || d.Count != 3 || d.RowCount != 1 || len(d.ByMethod) != 2 {
		t.Fatalf("sum %d count %d rows %d methods %v", d.Sum, d.Count, d.RowCount, d.ByMethod)
	}
	if d := get("/?from=" + day + "&to=" + day + "&type=Hotspot"); d.Sum != 5000 {
		t.Fatalf("type filter must hide deposits, sum %d", d.Sum)
	}
	// the dashboard / daily summary query and the CSV export agree with the report
	if n, err := s.queries.SumTransactionsBetween(t.Context(), db.SumTransactionsBetweenParams{FromTs: 0, ToTs: 2000}); err != nil || n != 9000 {
		t.Fatalf("dashboard income %d %v", n, err)
	}
	w := httptest.NewRecorder()
	s.reportExport(w, httptest.NewRequest("GET", "/?from="+day+"&to="+day, nil))
	if body := w.Body.String(); strings.Count(body, "\n") != 4 || !strings.Contains(body, "AGENT-") || strings.Contains(body, "Voucher - AG1") {
		t.Fatalf("csv:\n%s", body)
	}
}
