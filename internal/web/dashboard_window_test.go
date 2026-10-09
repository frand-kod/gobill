package web

import (
	"encoding/json"
	"testing"
	"time"
)

// The charts show the last 12 months, oldest first, across a year boundary, with the same income rules as the tiles.
func TestDashboardRolling12Months(t *testing.T) {
	s, _ := newTestApp(t)
	loc := s.location()
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, loc)
	add := func(method string, price int64, at time.Time) {
		if _, err := s.conn.Exec(`INSERT INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
			VALUES (?, 'u', 'p', 'r', 'PPPoE', ?, ?, ?, 0, 0)`, method+at.String(), price, method, at.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	add("Cash - A", 1000, time.Date(2025, 3, 5, 9, 0, 0, 0, loc)) // oldest month in the window
	add("Cash - A", 2000, time.Date(2025, 12, 31, 23, 0, 0, 0, loc))
	add("Cash - A", 4000, time.Date(2026, 2, 1, 0, 30, 0, 0, loc))            // current month
	add("Cash - A", 8000, time.Date(2025, 2, 20, 9, 0, 0, 0, loc))            // 13 months back: outside
	add("Customer - Balance", 16000, time.Date(2025, 12, 5, 9, 0, 0, 0, loc)) // paid from balance: excluded
	d, err := s.dashboardData(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	var sales []int64
	if json.Unmarshal([]byte(d.Labels), &labels) != nil || json.Unmarshal([]byte(d.Sales), &sales) != nil {
		t.Fatal("bad json")
	}
	if len(labels) != 12 || len(sales) != 12 {
		t.Fatalf("%d labels, %d sales", len(labels), len(sales))
	}
	if labels[0][len(labels[0])-2:] != "25" || labels[11][len(labels[11])-2:] != "26" {
		t.Fatalf("labels %v", labels)
	}
	if sales[0] != 1000 || sales[9] != 2000 || sales[11] != 4000 {
		t.Fatalf("sales %v", sales)
	}
}
