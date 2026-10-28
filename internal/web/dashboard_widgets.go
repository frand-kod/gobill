package web

// Dashboard widgets: expiring-customer rows, the usage card and the activity cards.

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// dashWidgets holds the cards below the dashboard tiles and charts (old PHP widgets W4, W5, W6, W7, W8/W9).
type dashWidgets struct {
	Expiring []expiringRow // active, ending in the next 7 days, soonest first
	Recent   []expiringRow // ended in the last 7 days and not renewed, newest first
	Usage    usageCard
	Logs     []db.ActivityLog // the newest dashLogLimit entries
}

// usageCard is the Usage card: the same data over the last 7 and the last 30 days.
type usageCard struct {
	Week, Month usageWin
}

// usageWin is one window of the Usage card. Empty means no RADIUS usage in the window at all.
type usageWin struct {
	Total, Down, Up     string
	TotalNum, TotalUnit string // Total split for the big number
	Bars                []dayBar
	Top                 []usageTop
	Empty               bool
}

// usageTop is one of the heaviest users; Href is "" when no customer has that username.
type usageTop struct {
	Name, Href, Total string
	Pct               int // share of the heaviest user's total, for the thin bar
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
	Expires    string // full date and time, the tooltip of When
	When       string // "3 hours 20 minutes left" for Expiring, "2 days ago" for Recent
	Phone      string
	WA         string // wa.me link, "" when the phone is missing or too short
	Days       int    // calendar days from today in the app zone: ahead for Expiring, back for Recent (0 = today)
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
	words := s.pTexts()
	soon, err := s.queries.ListExpiringSubscriptions(ctx, db.ListExpiringSubscriptionsParams{
		Since: now.Unix(), Until: nextWeek, PageLimit: dashListLimit})
	if err != nil {
		return
	}
	for _, r := range soon {
		x := s.expiringRow(settings, r.CustomerID, r.Username, r.Fullname, r.Phone, r.PlanName, r.ExpiresAt)
		x.Days = int(dayIndex(time.Unix(r.ExpiresAt, 0), loc) - today)
		x.When = words.span(r.ExpiresAt-now.Unix()) + " " + words.Left
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
		x.When = words.span(now.Unix()-r.ExpiresAt) + " " + s.catalog.T(s.language(), "ago")
		w.Recent = append(w.Recent, x)
	}
	if w.Usage.Week, err = s.usageWindow(ctx, now, loc, 7); err != nil {
		return
	}
	if w.Usage.Month, err = s.usageWindow(ctx, now, loc, 30); err != nil {
		return
	}
	if w.Logs, err = s.queries.ListActivityLogs(ctx, db.ListActivityLogsParams{Limit: dashLogLimit}); err != nil {
		return
	}
	return
}

// dashListLimit caps the Expiring and Recent lists; the tab counts are the rows shown.
const dashListLimit = 50

// dashLogLimit is how many activity log rows the dashboard card shows; the See all link opens the rest.
const dashLogLimit = 6

// usageTopN is how many heaviest users the Usage card lists.
const usageTopN = 3

// usageWindow reads the RADIUS octets of all users over the last n days (radius_usage_daily).
func (s *Server) usageWindow(ctx context.Context, now time.Time, loc *time.Location, n int) (usageWin, error) {
	since := dayStart(now.Unix(), loc).AddDate(0, 0, -(n - 1)).Format("2006-01-02")
	sums, err := s.queries.SumRadiusUsageDailyByDay(ctx, since)
	if err != nil {
		return usageWin{}, err
	}
	if len(sums) == 0 {
		return usageWin{Empty: true}, nil
	}
	totals := make([]dayTotal, len(sums))
	var down, up int64
	for i, x := range sums {
		totals[i] = dayTotal{Day: x.Day, Input: x.Input, Output: x.Output}
		down += x.Output
		up += x.Input
	}
	num, unit := humanBytesParts(down + up)
	w := usageWin{Total: num + " " + unit, TotalNum: num, TotalUnit: unit, Down: humanBytes(down), Up: humanBytes(up),
		Bars: chartBars(dailyUsage(totals, n, now.Unix(), loc))}
	top, err := s.queries.TopRadiusUsageDaily(ctx, db.TopRadiusUsageDailyParams{Since: since, RowLimit: usageTopN})
	if err != nil {
		return usageWin{}, err
	}
	// the query orders by total, so the first row is the heaviest user and fills the thin bars
	peak := int64(1)
	if len(top) > 0 {
		peak = max(peak, top[0].Input+top[0].Output)
	}
	for _, t := range top {
		sum := t.Input + t.Output
		u := usageTop{Name: t.Username, Total: humanBytes(sum), Pct: int(sum * 100 / peak)}
		c, err := s.queries.GetCustomerByUsername(ctx, t.Username)
		switch {
		case err == nil:
			u.Href = "/admin/customers/" + strconv.FormatInt(c.ID, 10)
		case !errors.Is(err, sql.ErrNoRows):
			return usageWin{}, err
		}
		w.Top = append(w.Top, u)
	}
	return w, nil
}

// expiringRow builds one dashboard row; the WhatsApp reminder link is set only when the phone number allows one.
func (s *Server) expiringRow(settings map[string]string, customerID int64, username, fullname, phone, plan string, expiresAt int64) expiringRow {
	x := expiringRow{CustomerID: customerID, Username: username, Fullname: fullname, Phone: phone, Plan: plan, Expires: s.ts(expiresAt)}
	if l := waLink(phone, settings["country_code_phone"]); l != "" {
		x.WA = l + "?text=" + url.QueryEscape(s.catalog.T(s.language(), "Hello, your internet plan is about to end. Please renew to stay online."))
	}
	return x
}
