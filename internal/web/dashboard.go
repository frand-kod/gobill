package web

// Admin dashboard page and its data, and the admin bell (notices shown on every admin page).

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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
	SalesTotal               int64  // Sales summed over the 12 months
	Network                  netCard
	dashWidgets
}

// attnItem is one admin bell row: Kind is its English label (the template translates it), Count how many,
// Href where it leads. Err marks a service that is down (red); Mgr hides it below Admin.
type attnItem struct {
	Kind  string
	Count int
	Href  string
	Err   bool
	Mgr   bool
}

const (
	lowVoucherStock = 5                // plans with fewer unused vouchers than this are flagged
	expiryJobStale  = 15 * time.Minute // the expiry job writes expiry_last_run every minute
	// noticeCacheFor: the bell counts are reused this long, so admin pages do not each rerun the lists.
	// ponytail: one global 30 s cache; a per-admin or pushed count is the upgrade if a 30 s lag matters.
	noticeCacheFor = 30 * time.Second
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
		d.SalesTotal += sales[m]
	}
	d.Labels, d.Regs, d.Sales = jsonStr(labels), jsonStr(regs), jsonStr(sales)
	if d.dashWidgets, err = s.widgetData(ctx, now); err != nil {
		return
	}
	if d.Network, err = s.networkCard(ctx, now); err != nil {
		return
	}
	return
}

// bellItems returns the bell notices before the role filter. The counts are cached on the Server for
// noticeCacheFor, so admin pages do not each rerun the queries.
func (s *Server) bellItems(ctx context.Context, now time.Time) ([]attnItem, error) {
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	if !s.noticeAt.IsZero() && now.Sub(s.noticeAt) < noticeCacheFor {
		return s.noticeItems, nil
	}
	items, err := s.noticeCounts(ctx, now)
	if err != nil {
		return nil, err
	}
	s.noticeAt, s.noticeItems = now, items
	return items, nil
}

// noticeCounts runs the queries behind the bell. An item appears only when its count is above zero.
func (s *Server) noticeCounts(ctx context.Context, now time.Time) ([]attnItem, error) {
	loc := s.location()
	now = now.In(loc)
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, loc)
	endToday := midnight.AddDate(0, 0, 1).Unix() - 1
	// "expired in the last 3 days": today and the two calendar days before it
	threeDaysAgo := midnight.AddDate(0, 0, -2).Unix()
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	net, err := s.networkCard(ctx, now)
	if err != nil {
		return nil, err
	}
	// ponytail: both lists are capped at dashListLimit rows by their queries, so the counts top out there; a COUNT query is the upgrade.
	ending, err := s.queries.ListExpiringSubscriptions(ctx, db.ListExpiringSubscriptionsParams{Since: now.Unix(), Until: endToday, PageLimit: dashListLimit})
	if err != nil {
		return nil, err
	}
	ended, err := s.queries.ListRecentlyExpiredSubscriptions(ctx, db.ListRecentlyExpiredSubscriptionsParams{Since: threeDaysAgo, Until: now.Unix(), PageLimit: dashListLimit})
	if err != nil {
		return nil, err
	}
	vouchers, err := s.queries.VoucherStockByPlan(ctx)
	if err != nil {
		return nil, err
	}
	var out []attnItem
	if net.Offline > 0 {
		out = append(out, attnItem{Kind: "Router/NAS offline", Count: net.Offline, Href: "/admin/routers", Err: true, Mgr: true})
	}
	if last := settings["expiry_last_run"]; last != "" {
		if t, err := time.ParseInLocation(time.DateTime, last, loc); err == nil && now.Sub(t) > expiryJobStale {
			out = append(out, attnItem{Kind: "Expiry job stalled", Count: 1, Href: "/admin/status", Err: true, Mgr: true})
		}
	}
	// the queries return only rows in these windows, so their lengths are the counts
	if n := len(ending); n > 0 {
		out = append(out, attnItem{Kind: "Plans ending today", Count: n, Href: "/admin#customers-expiring-0"})
	}
	if n := len(ended); n > 0 {
		out = append(out, attnItem{Kind: "Expired in the last 3 days, not renewed", Count: n, Href: "/admin#customers-expired-3"})
	}
	var low int
	var lowPlan int64
	for _, v := range vouchers {
		if v.Unused < lowVoucherStock && v.Unused+v.Used > 0 {
			low++
			lowPlan = v.ID
		}
	}
	if low > 0 {
		href := "/admin/vouchers"
		if low == 1 {
			href = "/admin/vouchers?plan=" + strconv.FormatInt(lowPlan, 10)
		}
		out = append(out, attnItem{Kind: "Voucher stock low", Count: low, Href: href})
	}
	return out, nil
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
