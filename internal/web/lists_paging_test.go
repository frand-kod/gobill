package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
)

// seedBandwidths inserts n bandwidth profiles named Bw-00, Bw-01, ...
func seedBandwidths(t *testing.T, s *Server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := s.conn.Exec(`INSERT INTO bandwidths (name, rate_down, rate_down_unit, rate_up, rate_up_unit, burst)
			VALUES (?, 10, 'Mbps', 10, 'Mbps', '')`, fmt.Sprintf("Bw-%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
}

// showing is the pager's row range text in the app language.
func showing(s *Server, from, to, total int) string {
	return fmt.Sprintf(s.catalog.T(s.language(), "Showing %d–%d of %d"), from, to, total)
}

// The bandwidth list has one edit link per row, so counting them counts the rows on the page.
func TestListCountAndPageSize(t *testing.T) {
	s, h, _, c := crudApp(t)
	seedBandwidths(t, s, 60)
	const base = "/admin/bandwidth"
	for _, tc := range []struct {
		target                string
		rows, from, to, total int
	}{
		{base, 20, 1, 20, 60},
		{base + "?per=50", 50, 1, 50, 60},
		{base + "?per=50&page=2", 10, 51, 60, 60},
		{base + "?per=100", 60, 1, 60, 60},
		{base + "?per=7", 20, 1, 20, 60}, // not a page size: the default
		{base + "?q=Bw-1", 10, 1, 10, 10},
		{base + "?q=Bw-1&per=50", 10, 1, 10, 10},
	} {
		body := do(h, "GET", tc.target, nil, c).Body.String()
		if n := strings.Count(body, "/edit\""); n != tc.rows {
			t.Errorf("%s: %d rows, want %d", tc.target, n, tc.rows)
		}
		if !strings.Contains(body, showing(s, tc.from, tc.to, tc.total)) {
			t.Errorf("%s: no %q in the page", tc.target, showing(s, tc.from, tc.to, tc.total))
		}
	}
}

// Page links and the size links keep the search and the page size.
func TestListPagerKeepsSizeAndSearch(t *testing.T) {
	s, h, _, c := crudApp(t)
	seedBandwidths(t, s, 60)
	body := do(h, "GET", "/admin/bandwidth?q=Bw-&per=50", nil, c).Body.String()
	if !strings.Contains(body, "page=2&amp;per=50&amp;q=Bw-") {
		t.Fatal("Next link lost the page size or the search")
	}
	if !strings.Contains(body, "per=100") {
		t.Fatal("page size links missing")
	}
}

// The report pager counts the rows the filter matches, not the rows on the page.
func TestReportPagerCountsMatchingRows(t *testing.T) {
	s, h, _, c := crudApp(t)
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	s.Billing = &billing.Service{Loc: jkt}
	seedMixed(t, s, jkt, 60)
	body := do(h, "GET", "/admin/reports/period?from=2026-03-09&to=2026-03-11&router=r2", nil, c).Body.String()
	if !strings.Contains(body, showing(s, 1, 20, 28)) {
		t.Fatalf("no %q in the report pager", showing(s, 1, 20, 28))
	}
}
