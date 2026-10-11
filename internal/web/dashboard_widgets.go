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
	Expiring    []expiringRow // active, ending in the next 7 days
	Recent      []expiringRow // ended in the last 3 days and not renewed
	Vouchers    []db.VoucherStockByPlanRow
	Logs        []db.ActivityLog
	ExpiryRun   string
	ReminderRun string
	Insight     string // JSON labels for the active/expired pie
	InsightVals string // JSON values for the active/expired pie
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

type expiringRow struct {
	CustomerID int64
	Username   string
	Fullname   string
	Plan       string
	Expires    string
	Phone      string
	WA         string // wa.me link, "" when the phone is missing or too short
	Today      bool   // ends before midnight tonight (app time zone)
}

// widgetData gathers the dashboard cards; active and expired are the subscription counts already on the page.
func (s *Server) widgetData(ctx context.Context, now time.Time, active, expired int64) (w dashWidgets, err error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return
	}
	soon, err := s.queries.ListExpiringSubscriptions(ctx, db.ListExpiringSubscriptionsParams{
		Since: now.Unix(), Until: now.AddDate(0, 0, 7).Unix(), PageLimit: 20})
	if err != nil {
		return
	}
	loc := s.location()
	today := now.In(loc)
	tomorrow := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, loc).Unix()
	for _, r := range soon {
		x := s.expiringRow(settings, r.CustomerID, r.Username, r.Fullname, r.Phone, r.PlanName, r.ExpiresAt)
		x.Today = r.ExpiresAt < tomorrow
		w.Expiring = append(w.Expiring, x)
	}
	ended, err := s.queries.ListRecentlyExpiredSubscriptions(ctx, db.ListRecentlyExpiredSubscriptionsParams{
		Since: now.AddDate(0, 0, -3).Unix(), Until: now.Unix(), PageLimit: 20})
	if err != nil {
		return
	}
	for _, r := range ended {
		w.Recent = append(w.Recent, s.expiringRow(settings, r.CustomerID, r.Username, r.Fullname, r.Phone, r.PlanName, r.ExpiresAt))
	}
	if w.Vouchers, err = s.queries.VoucherStockByPlan(ctx); err != nil {
		return
	}
	if w.Logs, err = s.queries.ListActivityLogs(ctx, db.ListActivityLogsParams{Limit: 10}); err != nil {
		return
	}
	w.ExpiryRun, w.ReminderRun = settings["expiry_last_run"], settings["reminder_last_run"]
	w.Insight = jsonStr([]string{s.catalog.T(s.language(), "Active"), s.catalog.T(s.language(), "Expired")})
	w.InsightVals = jsonStr([]int64{active, expired})
	return
}

// expiringRow is one dashboard row; the WhatsApp reminder link is set only when the phone number allows one.
func (s *Server) expiringRow(settings map[string]string, customerID int64, username, fullname, phone, plan string, expiresAt int64) expiringRow {
	x := expiringRow{CustomerID: customerID, Username: username, Fullname: fullname, Phone: phone, Plan: plan, Expires: s.ts(expiresAt)}
	if l := waLink(phone, settings["country_code_phone"]); l != "" {
		x.WA = l + "?text=" + url.QueryEscape(s.catalog.T(s.language(), "Hello, your internet plan is about to end. Please renew to stay online."))
	}
	return x
}
