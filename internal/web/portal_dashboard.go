package web

// Customer portal dashboard, orders, invoices and activations.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
)

// ---- portal ----

// pTrxPage is one page of the customer's orders or activations.
type pTrxPage struct {
	Rows []db.Transaction
	Nav  pageNav
}

// pageOf drops the extra row paging() asks for and sets the pager; base is the page's URL.
func pageOf(trx []db.Transaction, pg pager, total int64, base string) pTrxPage {
	more := len(trx) > pg.Per
	if more {
		trx = trx[:pg.Per]
	}
	return pTrxPage{Rows: trx, Nav: newPageNav(pg, total, len(trx), more, func(page, per int) string {
		return pageHref(base, url.Values{}, page, per)
	})}
}

// pActivations lists the customer's own plan purchases and activations, one page at a time.
func (s *Server) pActivations(w http.ResponseWriter, r *http.Request) {
	_, pg := paging(r)
	cust := sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}
	trx, err := s.queries.ListActivationsByCustomer(r.Context(), db.ListActivationsByCustomerParams{CustomerID: cust, Limit: pg.limit(), Offset: pg.offset()})
	if err != nil {
		s.fail(w, "portal activations", err)
		return
	}
	total, err := s.queries.CountActivationsByCustomer(r.Context(), cust)
	if err != nil {
		s.fail(w, "count portal activations", err)
		return
	}
	s.prender(w, r, 200, "p_activation", Page{Title: "Activation History", Data: pageOf(trx, pg, total, "/portal/activation")})
}

// ---- dashboard, orders ----

// pPlan is the active plan card. Left is the countdown as text; the Alpine ticker keeps it live.
type pPlan struct {
	Name    string
	Ends    string // expiry date and time
	Expires int64
	Pending bool // start_on_first_login: no countdown until the first login
	Left    string
	Pct     int // share of the validity used, 0..100
}

// pExtend is the renew button of an ended plan (POST /portal/extend/{ID}).
type pExtend struct {
	ID   int64
	Plan string
}

// pMeter is one usage row: a bar when the plan has a limit.
type pMeter struct {
	Text string
	Bar  bool
	Pct  int  // bar fill, 0..100
	Warn bool // above 80% of the limit
}

type pUsage struct {
	Note                string // why there are no meters: no plan, or the plan's usage is not tracked here
	TotalNum, TotalUnit string // data used this period, the big number: "112.61", "GB"
	LeftNum, LeftUnit   string // data left before the limit, "" without a data limit
	Limit               string // the data limit, "" without one
	Data                pMeter
	HasTime             bool // the plan has a time limit
	Time                pMeter
}

// pTotal is an upload and download sum, as text.
type pTotal struct{ Down, Up string }

// pSess is one row of the connection history.
type pSess struct {
	Start, For, Up, Down, MAC string
	Open                      bool
}

type pDashData struct {
	Plan      *pPlan   // nil when there is no active plan
	PlanEnded string   // no active plan: the last plan that ended, "" if none
	Extend    *pExtend // no active plan and extend_expired is on: renew the last ended plan
	Buy       bool     // the customer can pay here (a gateway, or balance to spend): show the buy button
	Usage     pUsage
	Live      pLive    // the live card: online now, else last seen
	Days      []dayBar // the 7-day usage chart
	Today     pTotal   // today's usage
	Period    pTotal   // usage since the plan started
	Words     pWords
	Now       int64
	Outage    bool // a mass drop covers this customer: the banner "outage in your area"
	Voucher   bool
	Company   string // operator contact from Settings
	Phone     string
	WA        string           // wa.me link of Phone, "" when it is not a usable number
	Trx       []db.Transaction // latest 3 orders, "See all" opens /portal/orders
}

// pWords are the countdown and duration words, translated. The Alpine ticker gets the same ones.
type pWords struct {
	Days, Hours, Minutes, Left, Done string
}

func (s *Server) pTexts() pWords {
	t := func(k string) string { return s.catalog.T(s.language(), k) }
	return pWords{t("days"), t("hours"), t("minutes"), t("left"), t("Ended")}
}

// JSON is the words as a JSON object for the ticker's x-data.
func (w pWords) JSON() string {
	b, _ := json.Marshal(map[string]string{"days": w.Days, "hours": w.Hours, "minutes": w.Minutes, "left": w.Left, "done": w.Done})
	return string(b)
}

// span: "3 hari 4 jam", "2 jam 15 menit", "45 menit". The ticker's gbSpan in usage.js does the same.
func (w pWords) span(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	d, h, m := sec/86400, sec%86400/3600, sec%3600/60
	switch {
	case d > 0:
		if h > 0 {
			return fmt.Sprintf("%d %s %d %s", d, w.Days, h, w.Hours)
		}
		return fmt.Sprintf("%d %s", d, w.Days)
	case h > 0:
		if m > 0 {
			return fmt.Sprintf("%d %s %d %s", h, w.Hours, m, w.Minutes)
		}
		return fmt.Sprintf("%d %s", h, w.Hours)
	case sec > 0 && m == 0:
		m = 1 // under a minute still reads as one minute
	}
	return fmt.Sprintf("%d %s", m, w.Minutes)
}

// countdown: "3 hari 4 jam lagi", or the done word once the time is up.
func (w pWords) countdown(sec int64) string {
	if sec <= 0 {
		return w.Done
	}
	return w.span(sec) + " " + w.Left
}

// maskMAC keeps the vendor part and the last two octets: CE:33:••:••:2A:AA. "" when it is not a MAC.
func maskMAC(mac string) string {
	p := strings.FieldsFunc(mac, func(r rune) bool { return r == ':' || r == '-' })
	if len(p) != 6 {
		return ""
	}
	p[2], p[3] = "••", "••"
	return strings.Join(p, ":")
}

// loginNames: the RADIUS user names of the customer (username, and the PPPoE name when it differs).
func loginNames(c db.Customer) []string {
	names := []string{c.Username}
	if c.PppoeUsername != "" && c.PppoeUsername != c.Username {
		names = append(names, c.PppoeUsername)
	}
	return names
}

// names2 are the two login names the queries take: the same name twice when there is only one.
func names2(names []string) (string, string) {
	return names[0], names[len(names)-1]
}

func (s *Server) pDashboard(w http.ResponseWriter, r *http.Request) { s.pDash(w, r, 200, "") }

func (s *Server) pDash(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	c := customerFrom(r)
	ctx := r.Context()
	st, err := s.loadSettings(ctx)
	if err != nil {
		s.fail(w, "portal dashboard", err)
		return
	}
	subs, err := s.queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
	if err != nil {
		s.fail(w, "portal dashboard", err)
		return
	}
	now := time.Now().Unix()
	names := loginNames(*c)
	n1, n2 := names2(names)
	d := pDashData{Words: s.pTexts(), Now: now, Voucher: st["disable_voucher"] != "yes",
		Company: st["company_name"], Phone: st["phone"], WA: waLink(st["phone"], st["country_code_phone"]),
		Buy: st["payment_gateway"] != "" || st["enable_balance"] != "no" && c.Balance > 0}

	// the active plan: the one that ends last
	var sub *db.Subscription
	for i := range subs {
		if subs[i].Status == "active" && (subs[i].ExpiresAt > now || subs[i].PendingStart == 1) && (sub == nil || subs[i].ExpiresAt > sub.ExpiresAt) {
			sub = &subs[i]
		}
	}
	if sub != nil {
		p, _ := s.queries.GetPlan(ctx, sub.PlanID)
		left := sub.ExpiresAt - now
		d.Plan = &pPlan{Name: p.Name, Ends: s.subExpiry(sub.PendingStart, sub.ExpiresAt), Expires: sub.ExpiresAt, Left: d.Words.countdown(left),
			Pct: validityPct(sub.StartedAt, sub.ExpiresAt, now)}
		if sub.PendingStart == 1 {
			d.Plan.Pending, d.Plan.Left, d.Plan.Pct = true, s.catalog.T(s.language(), "Starts on first login"), 0
		}
		oct, err := s.queries.SumRadiusOctetsByUsers(ctx, db.SumRadiusOctetsByUsersParams{Name1: n1, Name2: n2, StartedAt: sub.StartedAt})
		if err != nil {
			s.fail(w, "portal usage", err)
			return
		}
		d.Period = pTotal{Down: humanBytes(oct.Output), Up: humanBytes(oct.Input)}
		d.Usage = s.pUsage(ctx, *c, *sub, p, d.Words, oct.Input+oct.Output)
	} else {
		d.Usage.Note = s.catalog.T(s.language(), "No active plan")
		if len(subs) > 0 { // only finished plans: say which one ended
			p, _ := s.queries.GetPlan(ctx, subs[0].PlanID)
			d.PlanEnded = fmt.Sprintf(s.catalog.T(s.language(), "Plan %s ended on %s"), p.Name, s.ts(subs[0].ExpiresAt))
		}
		canExtend := (st["extend_expired"] == "1" || st["extend_expired"] == "yes") && c.Status == "Active"
		for _, x := range subs { // newest first: the last plan that is not active
			if x.Status != "active" {
				if p, _ := s.queries.GetPlan(ctx, x.PlanID); canExtend && billing.SellablePlan(p) {
					d.Extend = &pExtend{ID: x.ID, Plan: p.Name}
				}
				break
			}
		}
	}

	if d.Live, err = s.pLiveOf(ctx, *c, now, d.Words); err != nil {
		s.fail(w, "portal connection", err)
		return
	}
	// the chart: the last 7 days; today is the last bar
	rows, err := s.queries.ListRadiusUsageDaily(ctx, db.ListRadiusUsageDailyParams{Name1: n1, Name2: n2, Since: weekSince(now, s.location())})
	if err != nil {
		s.fail(w, "portal usage chart", err)
		return
	}
	totals := make([]dayTotal, len(rows))
	for i, x := range rows {
		totals[i] = dayTotal{Day: x.Day, Input: x.Input, Output: x.Output}
	}
	days := dailyUsage(totals, 7, now, s.location())
	d.Days = chartBars(days)
	d.Today = pTotal{Down: humanBytes(days[len(days)-1].Down), Up: humanBytes(days[len(days)-1].Up)}

	d.Trx, err = s.queries.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 3})
	if err != nil {
		s.fail(w, "portal dashboard orders", err)
		return
	}
	if d.Outage, err = billing.MassDropNow(ctx, s.queries, st, c.Username, time.Unix(now, 0)); err != nil {
		s.fail(w, "portal outage", err)
		return
	}
	s.prender(w, r, code, "p_dashboard", Page{Title: "Dashboard", Error: errMsg, Data: d})
}

// validityPct is the share of the subscription's validity that has passed, 0..100.
func validityPct(start, end, now int64) int {
	if end <= start {
		return 0
	}
	return int(min(max((now-start)*100/(end-start), 0), 100))
}

// pUsage: data and time used since the subscription started, the same sums the RADIUS login check uses.
// used is the data octets of the same period; it is shown when the plan has no data limit.
func (s *Server) pUsage(ctx context.Context, c db.Customer, sub db.Subscription, p db.Plan, w pWords, used int64) pUsage {
	if p.Limited == 1 && p.Device != "Radius" {
		return pUsage{Note: s.catalog.T(s.language(), "Usage is not recorded for this plan")}
	}
	q := s.planQuota(ctx, c, sub, p)
	u := pUsage{}
	u.TotalNum, u.TotalUnit = humanBytesParts(used)
	if q.HasData {
		u.Data = meter(q.Data, q.DataLim, humanBytes)
		u.LeftNum, u.LeftUnit = humanBytesParts(max(q.DataLim-q.Data, 0))
		u.Limit = humanBytes(q.DataLim)
	}
	if q.HasTime {
		u.HasTime = true
		u.Time = meter(q.Time, q.TimeLim, w.span)
	}
	return u
}

// meter fills a bar: used over limit, warn above 80%.
func meter(used, lim int64, text func(int64) string) pMeter {
	pct := min(used*100/lim, 100)
	return pMeter{Text: text(used) + " / " + text(lim), Bar: true, Pct: int(pct), Warn: used*100 > lim*80}
}

// pSession maps one RADIUS session to a history row; the duration of an open session runs to now.
func (s *Server) pSession(x db.RadiusSession, now int64, w pWords) pSess {
	end := now
	if x.StoppedAt.Valid {
		end = x.StoppedAt.Int64
	}
	return pSess{Start: s.ts(x.StartedAt), For: w.span(end - x.StartedAt), Up: humanBytes(x.InputOctets), Down: humanBytes(x.OutputOctets),
		MAC: maskMAC(x.Mac), Open: !x.StoppedAt.Valid && now-x.UpdatedAt <= radius.StaleFor(x.NasIp)}
}

// pOrders lists the customer's own transactions, perPage at a time.
func (s *Server) pOrders(w http.ResponseWriter, r *http.Request) {
	_, pg := paging(r)
	cust := sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}
	trx, err := s.queries.ListTransactionsByCustomer(r.Context(), db.ListTransactionsByCustomerParams{CustomerID: cust, Limit: pg.limit(), Offset: pg.offset()})
	if err != nil {
		s.fail(w, "portal orders", err)
		return
	}
	total, err := s.queries.CountTransactionsByCustomer(r.Context(), cust)
	if err != nil {
		s.fail(w, "count portal orders", err)
		return
	}
	s.prender(w, r, 200, "p_orders", Page{Title: "Order History", Data: pageOf(trx, pg, total, "/portal/orders")})
}

// pInvoice shows the invoice only for the customer's own transaction.
func (s *Server) pInvoice(w http.ResponseWriter, r *http.Request) {
	t, err := s.queries.GetTransaction(r.Context(), pathID(r))
	if err != nil || !t.CustomerID.Valid || t.CustomerID.Int64 != customerFrom(r).ID {
		http.NotFound(w, r)
		return
	}
	s.invoice(w, r, t)
}

// useWin is one window of the customer's daily usage chart: its total and its bars.
type useWin struct {
	Total string
	Bars  []dayBar
}

// useLast is the newest session in one line: start, duration and the sum of both directions.
type useLast struct{ Start, For, Total string }

type radiusUsage struct {
	Total, Unit string    // data since the plan started, the big number and its unit
	Empty       bool      // no daily usage and no session recorded
	Week, Month useWin    // the 7 and 30 day charts, built from one query
	Last        *useLast  // nil when there is no session
	Rows        []sessRow // the last 10 sessions, under Session history
}

// radiusUsage: bytes since the active subscription started, the daily chart for 7 and 30 days, and the
// last 10 sessions.
func (s *Server) radiusUsage(ctx context.Context, c db.Customer) (*radiusUsage, error) {
	u := &radiusUsage{}
	var total int64
	if p, err := s.queries.GetRadiusPlan(ctx, c.ID); err == nil {
		total, _ = s.queries.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: c.Username, StartedAt: p.StartedAt})
	}
	u.Total, u.Unit = humanBytesParts(total)
	now, loc := time.Now().Unix(), s.location()
	n1, n2 := names2(loginNames(c))
	rows, err := s.queries.ListRadiusUsageDaily(ctx, db.ListRadiusUsageDailyParams{Name1: n1, Name2: n2, Since: dayStart(now, loc).AddDate(0, 0, -29).Format("2006-01-02")})
	if err != nil {
		return nil, err
	}
	ss, err := s.queries.ListRecentRadiusSessionsByUser(ctx, c.Username)
	if err != nil {
		return nil, err
	}
	u.Empty = len(rows) == 0 && len(ss) == 0
	totals := make([]dayTotal, len(rows))
	for i, x := range rows {
		totals[i] = dayTotal{Day: x.Day, Input: x.Input, Output: x.Output}
	}
	u.Week = useWindow(dailyUsage(totals, 7, now, loc))
	u.Month = useWindow(dailyUsage(totals, 30, now, loc))
	if len(ss) > 0 {
		r := s.sessRows(ss[:1], now)[0]
		u.Last = &useLast{Start: r.Start, For: r.For, Total: humanBytes(ss[0].InputOctets + ss[0].OutputOctets)}
	}
	u.Rows = s.sessRows(ss, now)
	return u, nil
}

// useWindow sums one window of the daily chart and lays out its bars.
func useWindow(days []dayUse) useWin {
	var n int64
	for _, d := range days {
		n += d.Down + d.Up
	}
	return useWin{Total: humanBytes(n), Bars: chartBars(days)}
}
