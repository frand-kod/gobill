package web

import (
	"strings"
	"testing"
)

func seedRaw(t *testing.T, s *Server, inv, router, method string, price int64) {
	t.Helper()
	if _, err := s.conn.Exec(`INSERT INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
		VALUES (?, 'u', 'p', ?, 'Hotspot', ?, ?, 1000, 0, 0)`, inv, router, price, method); err != nil {
		t.Fatal(err)
	}
}

const repDay = "1970-01-01"

func TestReportRouterSentinels(t *testing.T) {
	s, h, _, c := crudApp(t)
	s.Lang.Store("english")
	seedRaw(t, s, "INV-R1", "", "Voucher - X", 100)
	seedRaw(t, s, "INV-B1", "balance", "Bank - Y", 200)
	seedRaw(t, s, "INV-M1", "mikro", "Admin - Cash", 400)
	page := func(router string) string {
		return do(h, "GET", "/admin/reports?date="+repDay+"&router="+router, nil, c).Body.String()
	}
	body := page("-radius")
	if !strings.Contains(body, "INV-R1") || strings.Contains(body, "INV-B1") || strings.Contains(body, "INV-M1") || !strings.Contains(body, "Rp 100") {
		t.Fatalf("radius filter:\n%s", body)
	}
	body = page("balance")
	if !strings.Contains(body, "INV-B1") || strings.Contains(body, "INV-R1") || strings.Contains(body, "INV-M1") || !strings.Contains(body, "Rp 200") {
		t.Fatalf("balance filter:\n%s", body)
	}
	if body := page(""); !strings.Contains(body, "INV-R1") || !strings.Contains(body, "INV-M1") || !strings.Contains(body, "RADIUS (no router)") {
		t.Fatal("all routers or option missing")
	}
}

func TestReportReportRoleHasNoInvoiceLinks(t *testing.T) {
	s, h, _, c := crudApp(t)
	s.Lang.Store("english")
	seedRaw(t, s, "INV-L1", "mikro", "Admin - Cash", 100)
	url := "/admin/reports?date=" + repDay
	if b := do(h, "GET", url, nil, c).Body.String(); !strings.Contains(b, `/invoice"`) {
		t.Fatal("admin must get the invoice link")
	}
	b := do(h, "GET", url, nil, login(t, h, "rita")).Body.String()
	if strings.Contains(b, "/invoice") || !strings.Contains(b, "INV-L1") {
		t.Fatalf("report role page:\n%s", b)
	}
}

func TestReportBackwardsRangeSwaps(t *testing.T) {
	s, h, _, c := crudApp(t)
	s.Lang.Store("english")
	seedRaw(t, s, "INV-S1", "mikro", "Admin - Cash", 100)
	seedRaw(t, s, "INV-S2", "mikro", "Admin - Cash", 50)
	a := do(h, "GET", "/admin/reports/period?from="+repDay+"&to=1970-01-02", nil, c).Body.String()
	b := do(h, "GET", "/admin/reports/period?from=1970-01-02&to="+repDay, nil, c).Body.String()
	if !strings.Contains(a, "Rp 150") || !strings.Contains(b, "Rp 150") || !strings.Contains(b, `value="`+repDay+`"`) {
		t.Fatalf("swap:\n%s", b)
	}
}

func TestReportMethodSelect(t *testing.T) {
	s, h, _, c := crudApp(t)
	s.Lang.Store("english")
	seedRaw(t, s, "INV-X1", "mikro", "Admin - Cash", 100)
	seedRaw(t, s, "INV-X2", "mikro", "Bank - BCA", 40)
	b := do(h, "GET", "/admin/reports?date="+repDay+"&method=Bank+-+BCA", nil, c).Body.String()
	if !strings.Contains(b, `<select class="input w-auto" name="method"`) || !strings.Contains(b, `<option value="Admin - Cash">`) ||
		!strings.Contains(b, `<option value="Bank - BCA" selected>`) || !strings.Contains(b, `value="Agent - Deposit"`) {
		t.Fatalf("method select:\n%s", b)
	}
	if !strings.Contains(b, "INV-X2") || strings.Contains(b, "INV-X1") {
		t.Fatal("method filter broke")
	}
}

func TestReportPrintLayout(t *testing.T) {
	s, h, _, c := crudApp(t)
	s.Lang.Store("english")
	seedRaw(t, s, "INV-P1", "", "Voucher - X", 100)
	seedRaw(t, s, "INV-P2", "mikro", "Admin - Cash", 300)
	p := do(h, "GET", "/admin/reports/print?date="+repDay+"&type=Hotspot", nil, c).Body.String()
	for _, want := range []string{"gobill", "01/01/1970", "All methods", "All routers", "All plans", "Total income", "Rp 400", "Rp 200", "Approved by", "RADIUS", "Printed at", "Printed by", "Daily Report"} {
		if !strings.Contains(p, want) {
			t.Errorf("print missing %q", want)
		}
	}
	if strings.Contains(p, "Expires On") || strings.Contains(p, "All types") {
		t.Error("print has Expires On or the wrong type words")
	}
}
