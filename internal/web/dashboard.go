package web

// Admin dashboard page and its data.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// ---- dashboard ----

type dashData struct {
	Warn                     string
	IncomeToday, IncomeMonth int64
	ActiveSubs, ExpiredSubs  int64
	Customers                int64
	OnlineNow                int64 // open RADIUS sessions, the tile links to the live page
	Year                     int
	Labels, Regs, Sales      string // JSON arrays for the charts
	Network                  netCard
	Attention                []attnItem // "Needs a look" strip, built last by dashboardData
	dashWidgets
}

// attnItem is one "Needs a look" row. Err marks a service that is affected (red); Mgr hides it below Admin.
type attnItem struct {
	Text, Href string
	Err        bool
	Mgr        bool
}

const (
	lowVoucherStock = 5                // plans with fewer unused vouchers than this are flagged
	expiryJobStale  = 15 * time.Minute // the expiry job writes expiry_last_run every minute
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := s.dashboardData(r.Context(), time.Now())
	if err != nil {
		s.fail(w, "dashboard", err)
		return
	}
	if s.ClockWarning != nil {
		d.Warn = s.ClockWarning()
	}
	d.Attention = attentionFor(d.Attention, adminFrom(r))
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
	if d.OnlineNow, err = s.queries.CountOpenRadiusSessions(ctx, ""); err != nil {
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
	if d.dashWidgets, err = s.widgetData(ctx, now); err != nil {
		return
	}
	if d.Network, err = s.networkCard(ctx, now); err != nil {
		return
	}
	d.Attention = s.attention(d, now, loc)
	return
}

// attention lists what needs an admin's eye, in display order. Each item appears only when its condition holds.
func (s *Server) attention(d dashData, now time.Time, loc *time.Location) []attnItem {
	var out []attnItem
	if d.Network.Offline > 0 {
		out = append(out, attnItem{Text: fmt.Sprintf(s.catalog.T(s.language(), "%d router/NAS offline"), d.Network.Offline), Href: "#network-title", Err: true, Mgr: true})
	}
	// ponytail: Expiring is capped at 20 rows by its query, so this count tops out at 20; a count query is the upgrade.
	today := 0
	for _, r := range d.Expiring {
		if r.Today {
			today++
		}
	}
	if today > 0 {
		out = append(out, attnItem{Text: fmt.Sprintf(s.catalog.T(s.language(), "%d plans end today"), today), Href: "/admin/subscriptions?status=active&sort=expires&dir=asc"})
	}
	if len(d.Recent) > 0 {
		out = append(out, attnItem{Text: fmt.Sprintf(s.catalog.T(s.language(), "%d expired in the last 3 days, not renewed"), len(d.Recent)), Href: "#recent-title"})
	}
	var low []string
	var lowPlan int64
	for _, v := range d.Vouchers {
		if v.Unused < lowVoucherStock && v.Unused+v.Used > 0 {
			low = append(low, v.Name+" ("+strconv.FormatInt(v.Unused, 10)+")")
			lowPlan = v.ID
		}
	}
	if len(low) > 0 {
		href := "/admin/vouchers"
		if len(low) == 1 {
			href = "/admin/vouchers?plan=" + strconv.FormatInt(lowPlan, 10)
		}
		out = append(out, attnItem{Text: s.catalog.T(s.language(), "Voucher stock low") + ": " + strings.Join(low, ", "), Href: href})
	}
	if last := d.ExpiryRun; last != "" {
		if t, err := time.ParseInLocation(time.DateTime, last, loc); err == nil && now.Sub(t) > expiryJobStale {
			out = append(out, attnItem{Text: fmt.Sprintf(s.catalog.T(s.language(), "Expiry job has not run since %s"), last), Href: "/admin/status", Err: true, Mgr: true})
		}
	}
	return out
}

// attentionFor drops the manager-only items unless the admin is an Admin or SuperAdmin.
func attentionFor(items []attnItem, a *db.Admin) []attnItem {
	mgr := a != nil && (a.Role == "SuperAdmin" || a.Role == "Admin")
	var out []attnItem
	for _, it := range items {
		if !it.Mgr || mgr {
			out = append(out, it)
		}
	}
	return out
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
