package web

// Dashboard widgets: expiring-customer rows and the activity cards.

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// dashWidgets holds the cards below the dashboard tiles and charts (old PHP widgets W4, W5, W6, W7, W8/W9).
type dashWidgets struct {
	Expiring  []expiringRow // active, ending in the next 7 days, soonest first
	Recent    []expiringRow // ended in the last 7 days and not renewed, newest first
	Vouchers  []db.VoucherStockByPlanRow
	Logs      []db.ActivityLog
	ExpiryRun string // expiry_last_run, read by the "Needs a look" strip
}

// waLink is the wa.me chat link for a phone number; numbers starting with 0 get the country code.
func waLink(phone, cc string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if strings.HasPrefix(strings.TrimSpace(phone), "0") && cc != "" {
		d = cc + strings.TrimLeft(d, "0")
	}
	if len(d) < 8 {
		return ""
	}
	return "https://wa.me/" + d
}

// expiringRow is one row of the Expiring and Recent lists.
type expiringRow struct {
	CustomerID int64
	Username   string
	Fullname   string
	Plan       string
	Expires    string
	Phone      string
	WA         string // wa.me link, "" when the phone is missing or too short
	Days       int    // calendar days from today in the app zone: ahead for Expiring, back for Recent (0 = today)
	Today      bool   // ends today (Days == 0)
}

// dayIndex is the calendar day of t in loc, counted in days since 1970-01-01 (DST does not skew it).
func dayIndex(t time.Time, loc *time.Location) int64 {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}

// widgetData gathers the dashboard cards.
func (s *Server) widgetData(ctx context.Context, now time.Time) (w dashWidgets, err error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return
	}
	loc := s.location()
	today := dayIndex(now, loc)
	y, m, d := now.In(loc).Date()
	// the lists cover whole calendar days: today and the six after it (expiring), the six before it (recent)
	nextWeek := time.Date(y, m, d+7, 0, 0, 0, 0, loc).Unix() - 1
	weekStart := time.Date(y, m, d-6, 0, 0, 0, 0, loc).Unix()
	soon, err := s.queries.ListExpiringSubscriptions(ctx, db.ListExpiringSubscriptionsParams{
		Since: now.Unix(), Until: nextWeek, PageLimit: dashListLimit})
	if err != nil {
		return
	}
	for _, r := range soon {
		x := s.expiringRow(settings, r.CustomerID, r.Username, r.Fullname, r.Phone, r.PlanName, r.ExpiresAt)
		x.Days = int(dayIndex(time.Unix(r.ExpiresAt, 0), loc) - today)
		x.Today = x.Days == 0
		w.Expiring = append(w.Expiring, x)
	}
	ended, err := s.queries.ListRecentlyExpiredSubscriptions(ctx, db.ListRecentlyExpiredSubscriptionsParams{
		Since: weekStart, Until: now.Unix(), PageLimit: dashListLimit})
	if err != nil {
		return
	}
	for _, r := range ended {
		x := s.expiringRow(settings, r.CustomerID, r.Username, r.Fullname, r.Phone, r.PlanName, r.ExpiresAt)
		x.Days = int(today - dayIndex(time.Unix(r.ExpiresAt, 0), loc))
		w.Recent = append(w.Recent, x)
	}
	if w.Vouchers, err = s.queries.VoucherStockByPlan(ctx); err != nil {
		return
	}
	if w.Logs, err = s.queries.ListActivityLogs(ctx, db.ListActivityLogsParams{Limit: 10}); err != nil {
		return
	}
	w.ExpiryRun = settings["expiry_last_run"]
	return
}

// dashListLimit caps the Expiring and Recent lists; the tab counts are the rows shown.
const dashListLimit = 50

// expiringRow builds one dashboard row; the WhatsApp reminder link is set only when the phone number allows one.
func (s *Server) expiringRow(settings map[string]string, customerID int64, username, fullname, phone, plan string, expiresAt int64) expiringRow {
	x := expiringRow{CustomerID: customerID, Username: username, Fullname: fullname, Phone: phone, Plan: plan, Expires: s.ts(expiresAt)}
	if l := waLink(phone, settings["country_code_phone"]); l != "" {
		x.WA = l + "?text=" + url.QueryEscape(s.catalog.T(s.language(), "Hello, your internet plan is about to end. Please renew to stay online."))
	}
	return x
}
