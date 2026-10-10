package web

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
)

// seedMixed inserts varied rows across three days: types, methods, routers and the excluded balance moves.
func seedMixed(t *testing.T, s *Server, jkt *time.Location, n int) {
	t.Helper()
	types := []string{"PPPoE", "Hotspot", "Balance"}
	methods := []string{"Admin - Cash", "Customer - Balance", "Cash - A", "Balance - Gift from x", ""}
	routers := []string{"r1", "r2"}
	for i := 0; i < n; i++ {
		at := time.Date(2026, 3, 9+i%3, 0, 0, 0, 0, jkt).Add(time.Duration(i*7) * time.Minute)
		if _, err := s.conn.Exec(`INSERT INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
			VALUES (?, 'u', 'p', ?, ?, ?, ?, ?, 0, 0)`,
			fmt.Sprintf("MIX-%04d", i), routers[i%2], types[i%3], int64(100+i*7), methods[i%5], at.Unix()); err != nil {
			t.Fatal(err)
		}
	}
}

// Totals from the SQL aggregates must equal the totals computed in Go from the full row list
// (the pre-change behaviour), for every filter, and the pages must concatenate to that list.
func TestReportTotalsMatchRowList(t *testing.T) {
	s, q := newTestApp(t)
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	s.Billing = &billing.Service{Loc: jkt}
	seedMixed(t, s, jkt, 60)
	for _, qs := range []string{
		"from=2026-03-09&to=2026-03-11",
		"from=2026-03-09&to=2026-03-11&type=PPPoE",
		"from=2026-03-09&to=2026-03-10&method=Cash+-+A",
		"from=2026-03-09&to=2026-03-11&router=r2",
		"from=2026-03-11&to=2026-03-09", // inverted range: nothing
	} {
		req := httptest.NewRequest("GET", "/admin/reports/period?"+qs, nil)
		d, a, err := s.reportLoad(req, true)
		if err != nil {
			t.Fatal(err)
		}
		all, err := q.ReportTransactions(t.Context(), db.ReportTransactionsParams(a))
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		byType, byMethod := map[string]*repTotal{}, map[string]*repTotal{}
		add := func(m map[string]*repTotal, k string, p int64) {
			if m[k] == nil {
				m[k] = &repTotal{Name: k}
			}
			m[k].Count++
			m[k].Sum += p
		}
		for _, r := range all {
			sum += r.Price
			add(byType, r.Type, r.Price)
			add(byMethod, r.Method, r.Price)
		}
		if d.Count != len(all) || d.Sum != sum {
			t.Fatalf("%s: totals %d/%d, want %d/%d", qs, d.Count, d.Sum, len(all), sum)
		}
		if len(d.ByType) != len(byType) || len(d.ByMethod) != len(byMethod) {
			t.Fatalf("%s: group counts differ", qs)
		}
		for _, g := range d.ByType {
			if w := byType[g.Name]; w == nil || w.Count != g.Count || w.Sum != g.Sum {
				t.Fatalf("%s: by type %q = %+v", qs, g.Name, g)
			}
		}
		for _, g := range d.ByMethod {
			if w := byMethod[g.Name]; w == nil || w.Count != g.Count || w.Sum != g.Sum {
				t.Fatalf("%s: by method %q = %+v", qs, g.Name, g)
			}
		}
		// pages of 20 concatenate to the full list, in the same order
		var paged []string
		for off := int64(0); ; off += perPage {
			rows, err := s.reportRows(t.Context(), a, perPage, off)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				paged = append(paged, r.Invoice)
			}
			if len(rows) < perPage {
				break
			}
		}
		if len(paged) != len(all) {
			t.Fatalf("%s: paged %d rows, want %d", qs, len(paged), len(all))
		}
		for i, r := range all {
			if paged[i] != r.Invoice {
				t.Fatalf("%s: row %d = %s, want %s", qs, i, paged[i], r.Invoice)
			}
		}
	}
}

// The report table is 20 rows a page; Prev/Next keep the filter; the totals still cover every row.
func TestReportPaginationKeepsFilter(t *testing.T) {
	s, h, _, c := crudApp(t)
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	s.Billing = &billing.Service{Loc: jkt}
	seedMixed(t, s, jkt, 60)
	target := "/admin/reports/period?from=2026-03-09&to=2026-03-11&router=r2"
	p1 := do(h, "GET", target, nil, c).Body.String()
	p2 := do(h, "GET", target+"&page=2", nil, c).Body.String()
	rowTag := `<a class="link" href="/admin/transactions/`
	if n := strings.Count(p1, rowTag); n != perPage {
		t.Fatalf("page 1 rows %d, want %d", n, perPage)
	}
	if !strings.Contains(p1, "page=2") {
		t.Fatal("page 1 has no Next link")
	}
	if !strings.Contains(p1, "router=r2") || !strings.Contains(p2, "router=r2") {
		t.Fatal("filter lost from page links")
	}
	if !strings.Contains(p2, "page=1") || strings.Contains(p2, "page=3") {
		t.Fatal("page 2 needs a Prev link and no Next link")
	}
	// 30 rows are on router r2; rows 11 and 41 are balance transfers and leave the report: 20 on page 1, 8 on page 2
	if n := strings.Count(p2, rowTag); n != 8 {
		t.Fatalf("page 2 rows %d, want 8", n)
	}
	// the totals show all matching rows, not the page
	if !strings.Contains(p2, "Total") {
		t.Fatal("total row missing")
	}
	req := httptest.NewRequest("GET", target, nil)
	d, _, err := s.reportLoad(req, true)
	if err != nil || d.Count != 28 {
		t.Fatalf("count %d %v, want 28", d.Count, err)
	}
}

// Print caps the rows and says so; CSV export still has every row.
func TestReportPrintCapAndExportAll(t *testing.T) {
	s, h, _, c := crudApp(t)
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	s.Billing = &billing.Service{Loc: jkt}
	tx, err := s.conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, jkt).Unix()
	for i := 0; i < reportPrintCap+1; i++ {
		if _, err := tx.Exec(`INSERT INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
			VALUES (?, 'u', 'p', 'r', 'PPPoE', 1, 'Cash', ?, 0, 0)`, fmt.Sprintf("BULK-%05d", i), at); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	p := do(h, "GET", "/admin/reports/print?date=2026-03-10", nil, c).Body.String()
	if !strings.Contains(p, "5000") {
		t.Fatal("print: truncation notice missing")
	}
	if strings.Contains(p, fmt.Sprintf("BULK-%05d", reportPrintCap)) {
		t.Fatal("print: row beyond the cap printed")
	}
	csv := do(h, "GET", "/admin/reports/export?date=2026-03-10", nil, c).Body.String()
	if n := strings.Count(csv, "\n"); n != reportPrintCap+2 { // header + all rows + trailing newline of last
		t.Fatalf("export lines %d, want %d", n, reportPrintCap+2)
	}
}

// Portal orders: 20 a page, own transactions only; dashboard shows the latest 10 with a link to all.
func TestPortalOrdersPaginationAndIsolation(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	other, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "U Two", ServiceType: "PPPoE", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		seedTrx(t, nil, e.q, e.cust.ID, fmt.Sprintf("MINE-%02d", i), "PPPoE", "Admin - Cash", 1000, time.Now())
	}
	for i := 0; i < 3; i++ {
		seedTrx(t, nil, e.q, other.ID, fmt.Sprintf("THEIRS-%02d", i), "PPPoE", "Admin - Cash", 1000, time.Now())
	}
	cc, _ := custLogin(t, e, "u1", "pw12345")

	p1 := do(e.h, "GET", "/portal/orders", nil, cc).Body.String()
	if n := strings.Count(p1, `invoice">MINE-`); n != perPage {
		t.Fatalf("orders page 1: %d rows, want %d", n, perPage)
	}
	if !strings.Contains(p1, `href="/portal/orders?page=2"`) || strings.Contains(p1, "THEIRS") {
		t.Fatal("orders page 1: next link or isolation wrong")
	}
	p2 := do(e.h, "GET", "/portal/orders?page=2", nil, cc).Body.String()
	if n := strings.Count(p2, `invoice">MINE-`); n != 5 || strings.Contains(p2, "THEIRS") || strings.Contains(p2, `page=3`) {
		t.Fatalf("orders page 2: %d rows", n)
	}
	if !strings.Contains(p2, `href="/portal/orders?page=1"`) {
		t.Fatal("orders page 2: no previous link")
	}

	dash := do(e.h, "GET", "/portal", nil, cc).Body.String()
	if !strings.Contains(dash, "MINE-24") || !strings.Contains(dash, "MINE-15") || strings.Contains(dash, "MINE-14") {
		t.Fatal("dashboard: latest 10 wrong")
	}
	if !strings.Contains(dash, `href="/portal/orders"`) || strings.Contains(dash, "THEIRS") {
		t.Fatal("dashboard: see-all link or isolation wrong")
	}
}

// Admin transactions: ?customer=ID shows only that customer's rows; the customer page links to it.
func TestAdminTransactionsCustomerFilter(t *testing.T) {
	_, h, q, c := crudApp(t)
	mk := func(name string) db.Customer {
		cu, err := q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: name, PasswordHash: "h", Fullname: name, ServiceType: "PPPoE", Status: "Active"})
		if err != nil {
			t.Fatal(err)
		}
		return cu
	}
	a, b := mk("cusa"), mk("cusb")
	seedTrx(t, nil, q, a.ID, "INV-CUSA", "PPPoE", "Admin - Cash", 100, time.Now())
	seedTrx(t, nil, q, b.ID, "INV-CUSB", "PPPoE", "Admin - Cash", 100, time.Now())
	filtered := do(h, "GET", fmt.Sprintf("/admin/transactions?customer=%d", a.ID), nil, c).Body.String()
	if !strings.Contains(filtered, "INV-CUSA") || strings.Contains(filtered, "INV-CUSB") {
		t.Fatal("customer filter: wrong rows")
	}
	if !strings.Contains(filtered, `value="`+fmt.Sprint(a.ID)+`" selected`) {
		t.Fatal("customer filter: not shown in the toolbar")
	}
	all := do(h, "GET", "/admin/transactions", nil, c).Body.String()
	if !strings.Contains(all, "INV-CUSA") || !strings.Contains(all, "INV-CUSB") {
		t.Fatal("unfiltered list lost rows")
	}
	detail := do(h, "GET", fmt.Sprintf("/admin/customers/%d", a.ID), nil, c).Body.String()
	if !strings.Contains(detail, fmt.Sprintf("/admin/transactions?customer=%d", a.ID)) {
		t.Fatal("customer page: no view-all link")
	}
}
