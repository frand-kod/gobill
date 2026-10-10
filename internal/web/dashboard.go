package web

// Admin dashboard page and its data.

import (
	"encoding/json"
	"net/http"

	"context"
	"github.com/frand-kod/gobill/internal/db"
	"strconv"
	"time"
)

// ---- dashboard ----

type dashData struct {
	Warn                     string
	IncomeToday, IncomeMonth int64
	ActiveSubs, ExpiredSubs  int64
	Customers                int64
	Year                     int
	Labels, Regs, Sales      string // JSON arrays for the charts
	Network                  netCard
	dashWidgets
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := s.dashboardData(r.Context(), time.Now())
	if err != nil {
		s.fail(w, "dashboard", err)
		return
	}
	if s.ClockWarning != nil {
		d.Warn = s.ClockWarning()
	}
	s.render(w, r, http.StatusOK, "dashboard", Page{
		Title: "Dashboard",
		Data:  d,
		Flash: s.sessions.PopString(r.Context(), "flash"),
	})
}

// dashboardData gathers the tiles and the per-month series of the last 12 months in the app location.
func (s *Server) dashboardData(ctx context.Context, now time.Time) (d dashData, err error) {
	loc := s.location()
	now = now.In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	// PHP dashboard.php:29-35: the "month" income runs from reset_day (default 1) to today.
	start := month
	if st, e := s.loadSettings(ctx); e == nil {
		if rd, _ := strconv.Atoi(st["reset_day"]); rd > 1 && rd <= 28 {
			start = month.AddDate(0, 0, rd-1)
			if now.Day() < rd {
				start = start.AddDate(0, -1, 0)
			}
		}
	}
	sum := func(from, to time.Time) (int64, error) {
		return s.queries.SumTransactionsBetween(ctx, db.SumTransactionsBetweenParams{CreatedAt: from.Unix(), CreatedAt_2: to.Unix()})
	}
	if d.IncomeToday, err = sum(day, day.AddDate(0, 0, 1)); err != nil {
		return
	}
	if d.IncomeMonth, err = sum(start, day.AddDate(0, 0, 1)); err != nil {
		return
	}
	if d.ActiveSubs, err = s.queries.CountSubscriptionsByStatus(ctx, "active"); err != nil {
		return
	}
	if d.ExpiredSubs, err = s.queries.CountSubscriptionsByStatus(ctx, "expired"); err != nil {
		return
	}
	if d.Customers, err = s.queries.CountCustomers(ctx); err != nil {
		return
	}
	d.Year = now.Year()
	labels, regs, sales := make([]string, 12), make([]int64, 12), make([]int64, 12)
	// rolling window: the last 12 months, oldest first, ending with the current month
	for m := 0; m < 12; m++ {
		from := month.AddDate(0, m-11, 0)
		to := from.AddDate(0, 1, 0)
		labels[m] = s.catalog.T(s.language(), from.Month().String()[:3]) + from.Format(" 06")
		if regs[m], err = s.queries.CountCustomersBetween(ctx, db.CountCustomersBetweenParams{CreatedAt: from.Unix(), CreatedAt_2: to.Unix()}); err != nil {
			return
		}
		if sales[m], err = sum(from, to); err != nil {
			return
		}
	}
	d.Labels, d.Regs, d.Sales = jsonStr(labels), jsonStr(regs), jsonStr(sales)
	if d.dashWidgets, err = s.widgetData(ctx, now, d.ActiveSubs, d.ExpiredSubs); err != nil {
		return
	}
	d.Network, err = s.networkCard(ctx, now)
	return
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
