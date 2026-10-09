package web

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
)

// seedTrx inserts a transaction at the given instant.
func seedTrx(t *testing.T, s *Server, q *db.Queries, cust int64, inv, typ, method string, price int64, at time.Time) db.Transaction {
	t.Helper()
	tx, err := q.CreateTransaction(t.Context(), db.CreateTransactionParams{Invoice: inv, CustomerID: sql.NullInt64{Int64: cust, Valid: true}, Username: "u1", PlanName: "P",
		Type: typ, Price: price, Method: method, PeriodStart: at.Unix(), PeriodEnd: at.Unix() + 86400})
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		return tx
	}
	if _, err := s.conn.Exec(`UPDATE transactions SET created_at = ? WHERE id = ?`, at.Unix(), tx.ID); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestReports(t *testing.T) {
	s, h, q, c := crudApp(t)
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	s.Billing = &billing.Service{Loc: jkt}
	cust, err := q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u1", PasswordHash: "h", Fullname: "U", ServiceType: "PPPoE", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	d := func(day, hh, mm int) time.Time { return time.Date(2026, 3, day, hh, mm, 0, 0, jkt) }
	seedTrx(t, s, q, cust.ID, "INV-A", "PPPoE", "Admin - Cash", 100, d(9, 23, 59)) // before the boundary
	seedTrx(t, s, q, cust.ID, "INV-B", "Hotspot", "Admin - Cash", 20, d(10, 0, 0)) // exactly local midnight (17:00 UTC prior day)
	seedTrx(t, s, q, cust.ID, "INV-C", "Balance", "=cmd|x", 3, d(10, 23, 59))
	seedTrx(t, s, q, cust.ID, "INV-D", "PPPoE", "Admin - Cash", 400, d(11, 0, 0))

	// daily total across the Jakarta midnight: only B and C on the 10th
	body := do(h, "GET", "/admin/reports?date=2026-03-10", nil, c).Body.String()
	if !strings.Contains(body, "INV-B") || !strings.Contains(body, "INV-C") || strings.Contains(body, "INV-A") || strings.Contains(body, "INV-D") {
		t.Fatalf("daily rows wrong:\n%s", body)
	}
	if !strings.Contains(body, "Rp 23") {
		t.Fatal("daily total missing")
	}
	// period, filtered by type
	body = do(h, "GET", "/admin/reports/period?from=2026-03-09&to=2026-03-11&type=PPPoE", nil, c).Body.String()
	if !strings.Contains(body, "INV-A") || !strings.Contains(body, "INV-D") || strings.Contains(body, "INV-B") || !strings.Contains(body, "Rp 500") {
		t.Fatalf("period/type filter wrong:\n%s", body)
	}
	// CSV matches the filter and defuses formulas
	w := do(h, "GET", "/admin/reports/export?from=2026-03-10&to=2026-03-10", nil, c)
	rows := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(rows) != 3 || !strings.Contains(w.Body.String(), "'=cmd|x") || strings.Contains(w.Body.String(), "INV-A") {
		t.Fatalf("csv:\n%s", w.Body.String())
	}
	// print page shows company and totals
	if p := do(h, "GET", "/admin/reports/print?date=2026-03-10", nil, c); p.Code != 200 || !strings.Contains(p.Body.String(), "NuxBill") || !strings.Contains(p.Body.String(), "Rp 23") {
		t.Fatalf("print: %d", p.Code)
	}
	// Report role can view
	rita := login(t, h, "rita")
	for _, u := range []string{"/admin/reports", "/admin/reports/period", "/admin/reports/export", "/admin/reports/print"} {
		wantCode(t, do(h, "GET", u, nil, rita), 200, u)
	}
	// invoice: number and price
	inv := do(h, "GET", "/admin/transactions/1/invoice", nil, c)
	if inv.Code != 200 || !strings.Contains(inv.Body.String(), "INV-A") || !strings.Contains(inv.Body.String(), "Rp 100") {
		t.Fatalf("invoice: %d", inv.Code)
	}
	wantCode(t, do(h, "GET", "/admin/transactions/999/invoice", nil, c), 404, "missing invoice")
}

func TestPortalInvoiceIsolation(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	other, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "U Two", ServiceType: "PPPoE", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	mine := seedTrx(t, nil, e.q, e.cust.ID, "INV-MINE", "PPPoE", "Admin - Cash", 5000, now)
	theirs := seedTrx(t, nil, e.q, other.ID, "INV-THEIRS", "PPPoE", "Admin - Cash", 7000, now)
	cc, _ := custLogin(t, e, "u1", "pw12345")
	if w := do(e.h, "GET", "/portal/orders/"+itoa(mine.ID)+"/invoice", nil, cc); w.Code != 200 || !strings.Contains(w.Body.String(), "INV-MINE") {
		t.Fatalf("own invoice: %d", w.Code)
	}
	wantCode(t, do(e.h, "GET", "/portal/orders/"+itoa(theirs.ID)+"/invoice", nil, cc), 404, "other customer's invoice")
}
