package web

import (
	"context"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// dashWidgets holds the cards below the dashboard tiles and charts (old PHP widgets W4, W5, W6, W7, W8/W9).
type dashWidgets struct {
	Expiring    []expiringRow
	Vouchers    []db.VoucherStockByPlanRow
	Logs        []db.ActivityLog
	ExpiryRun   string
	ReminderRun string
	Insight     string // JSON labels for the active/expired pie
	InsightVals string // JSON values for the active/expired pie
}

type expiringRow struct {
	CustomerID int64
	Username   string
	Fullname   string
	Plan       string
	Expires    string
	Expired    bool
}

// widgetData gathers the dashboard cards; active and expired are the subscription counts already on the page.
func (s *Server) widgetData(ctx context.Context, now time.Time, active, expired int64) (w dashWidgets, err error) {
	rows, err := s.queries.ListExpiringSubscriptions(ctx, db.ListExpiringSubscriptionsParams{
		Since: now.AddDate(0, 0, -1).Unix(), PageLimit: 20})
	if err != nil {
		return
	}
	for _, r := range rows {
		w.Expiring = append(w.Expiring, expiringRow{CustomerID: r.CustomerID, Username: r.Username, Fullname: r.Fullname,
			Plan: r.PlanName, Expires: s.ts(r.ExpiresAt), Expired: r.Status == "expired" || r.ExpiresAt < now.Unix()})
	}
	if w.Vouchers, err = s.queries.VoucherStockByPlan(ctx); err != nil {
		return
	}
	if w.Logs, err = s.queries.ListActivityLogs(ctx, db.ListActivityLogsParams{Limit: 10}); err != nil {
		return
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return
	}
	w.ExpiryRun, w.ReminderRun = settings["expiry_last_run"], settings["reminder_last_run"]
	w.Insight = jsonStr([]string{s.catalog.T(s.language(), "Active"), s.catalog.T(s.language(), "Expired")})
	w.InsightVals = jsonStr([]int64{active, expired})
	return
}
