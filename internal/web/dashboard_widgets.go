package web

import (
	"context"
	"net/url"
	"strings"
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
	Setup       []setupStep
	SetupDone   bool // every step is ticked: the checklist is hidden
}

// setupStep is one line of the dashboard "quick start" checklist.
type setupStep struct {
	Label, Href string
	Done        bool
}

// setupSteps ticks the first-run order: router or NAS, bandwidth, plan, then a customer or voucher.
func setupSteps(c db.CountSetupRow) (steps []setupStep, all bool) {
	steps = []setupStep{
		{"Add a router (or a NAS for RADIUS)", "/admin/routers/new", c.Routers+c.Nas > 0},
		{"Set a speed (Bandwidth)", "/admin/bandwidth/new", c.Bandwidths > 0},
		{"Create a plan", "/admin/plans/new", c.Plans > 0},
		{"Add a customer or make vouchers", "/admin/customers/new", c.Customers+c.Vouchers > 0},
	}
	all = true
	for _, s := range steps {
		all = all && s.Done
	}
	return
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
	Expired    bool
	Phone      string
	WA         string // wa.me link, "" when the phone is missing or too short
}

// widgetData gathers the dashboard cards; active and expired are the subscription counts already on the page.
func (s *Server) widgetData(ctx context.Context, now time.Time, active, expired int64) (w dashWidgets, err error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return
	}
	rows, err := s.queries.ListExpiringSubscriptions(ctx, db.ListExpiringSubscriptionsParams{
		Since: now.AddDate(0, 0, -1).Unix(), Until: now.AddDate(0, 0, 7).Unix(), PageLimit: 20})
	if err != nil {
		return
	}
	for _, r := range rows {
		x := expiringRow{CustomerID: r.CustomerID, Username: r.Username, Fullname: r.Fullname, Phone: r.Phone,
			Plan: r.PlanName, Expires: s.ts(r.ExpiresAt), Expired: r.Status == "expired" || r.ExpiresAt < now.Unix()}
		if l := waLink(r.Phone, settings["country_code_phone"]); l != "" {
			x.WA = l + "?text=" + url.QueryEscape(s.catalog.T(s.language(), "Hello, your internet plan is about to end. Please renew to stay online."))
		}
		w.Expiring = append(w.Expiring, x)
	}
	cnt, err := s.queries.CountSetup(ctx)
	if err != nil {
		return
	}
	w.Setup, w.SetupDone = setupSteps(cnt)
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
