package web

import (
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// S1: dashboard income leaves out balance-paid purchases, transfers and gifts, and the monthly
// figure starts at reset_day like PHP top_widget.
func TestDashboardIncome(t *testing.T) {
	s, q := newTestApp(t)
	ctx := t.Context()
	if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "reset_day", Value: "15"}); err != nil {
		t.Fatal(err)
	}
	loc := s.location()
	now := time.Date(2025, 3, 20, 12, 0, 0, 0, loc)
	add := func(method, typ string, price int64, at time.Time) {
		_, err := s.conn.Exec(`INSERT INTO transactions (invoice, username, plan_name, router_name, type, price, method, created_at, period_start, period_end)
			VALUES (?, 'u', 'p', 'r', ?, ?, ?, ?, 0, 0)`, method+at.String(), typ, price, method, at.Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	add("Cash - A", "PPPoE", 5000, time.Date(2025, 3, 10, 9, 0, 0, 0, loc)) // before reset_day
	add("Cash - A", "PPPoE", 10000, time.Date(2025, 3, 16, 9, 0, 0, 0, loc))
	add("Cash - A", "PPPoE", 4000, time.Date(2025, 3, 20, 9, 0, 0, 0, loc))
	add("Customer - Balance", "PPPoE", 7000, time.Date(2025, 3, 20, 10, 0, 0, 0, loc))      // paid from balance
	add("Customer - Balance", "Balance", 3000, time.Date(2025, 3, 20, 10, 5, 0, 0, loc))    // transfer row
	add("Balance - Gift from bob", "PPPoE", 2000, time.Date(2025, 3, 20, 10, 6, 0, 0, loc)) // gift
	d, err := s.dashboardData(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.IncomeToday != 4000 || d.IncomeMonth != 14000 {
		t.Fatalf("today %d month %d", d.IncomeToday, d.IncomeMonth)
	}
	// before reset_day the window starts in the previous month
	d, _ = s.dashboardData(ctx, time.Date(2025, 3, 20, 12, 0, 0, 0, loc).AddDate(0, 0, -6))
	if d.IncomeMonth != 5000 {
		t.Fatalf("month before reset %d", d.IncomeMonth)
	}
}
