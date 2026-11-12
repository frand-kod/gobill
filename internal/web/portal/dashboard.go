package portal

// Customer portal dashboard, orders, invoices and activations.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/service/diagnose"
)

// ---- portal ----

// pTrxPage is one page of the customer's orders or activations.
type pTrxPage struct {
	Pay  *db.PaymentRequest // the unpaid, unexpired online payment, nil when none
	Rows []db.Transaction
	Nav  view.PageNav
}

// pageOf drops the extra row view.Paging() asks for and sets the view.Pager; base is the page's URL.
func pageOf(trx []db.Transaction, pg view.Pager, total int64, base string) pTrxPage {
	more := len(trx) > pg.Per
	if more {
		trx = trx[:pg.Per]
	}
	return pTrxPage{Rows: trx, Nav: view.NewPageNav(pg, total, len(trx), more, func(page, per int) string {
		return view.PageHref(base, url.Values{}, page, per)
	})}
}

// pActivations lists the customer's own plan purchases and activations, one page at a time.
func (s *Handlers) pActivations(w http.ResponseWriter, r *http.Request) {
	_, pg := view.Paging(r)
	cust := sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}
	trx, err := s.Queries.ListActivationsByCustomer(r.Context(), db.ListActivationsByCustomerParams{CustomerID: cust, Limit: pg.Limit(), Offset: pg.Offset()})
	if err != nil {
		s.Fail(w, "portal activations", err)
		return
	}
	total, err := s.Queries.CountActivationsByCustomer(r.Context(), cust)
	if err != nil {
		s.Fail(w, "count portal activations", err)
		return
	}
	s.Prender(w, r, 200, "p_activation", core.Page{Title: "Activation History", Data: pageOf(trx, pg, total, "/portal/activation")})
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
	Live      PLive         // the live card: online now, else last seen
	Days      []core.DayBar // the 7-day usage chart
	Today     pTotal        // today's usage
	Period    pTotal        // usage since the plan started
	Words     core.PWords
	Now       int64
	Outage    bool // a mass drop covers this customer: the banner "outage in your area"
	Voucher   bool
	Company   string // operator contact from Settings
	Phone     string
	WA        string             // wa.me link of Phone, "" when it is not a usable number
	Trx       []db.Transaction   // latest 3 orders, "See all" opens /portal/orders
	Pay       *db.PaymentRequest // the unpaid, unexpired online payment, nil when none
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

func (s *Handlers) pDashboard(w http.ResponseWriter, r *http.Request) { s.pDash(w, r, 200, "") }

func (s *Handlers) pDash(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	c := customerFrom(r)
	ctx := r.Context()
	st, err := s.LoadSettings(ctx)
	if err != nil {
		s.Fail(w, "portal dashboard", err)
		return
	}
	subs, err := s.Queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
	if err != nil {
		s.Fail(w, "portal dashboard", err)
		return
	}
	now := time.Now().Unix()
	names := core.LoginNames(*c)
	n1, n2 := core.Names2(names)
	d := pDashData{Words: s.PTexts(), Now: now, Voucher: st["disable_voucher"] != "yes",
		Company: st["company_name"], Phone: st["phone"], WA: view.WaLink(st["phone"], st["country_code_phone"]),
		Buy: st["payment_gateway"] != "" || st["enable_balance"] != "no" && c.Balance > 0}

	// the active plan: the one that ends last
	var sub *db.Subscription
	for i := range subs {
		if subs[i].Status == "active" && (subs[i].ExpiresAt > now || subs[i].PendingStart == 1) && (sub == nil || subs[i].ExpiresAt > sub.ExpiresAt) {
			sub = &subs[i]
		}
	}
	if sub != nil {
		p, _ := s.Queries.GetPlan(ctx, sub.PlanID)
		left := sub.ExpiresAt - now
		d.Plan = &pPlan{Name: p.Name, Ends: s.SubExpiry(sub.PendingStart, sub.ExpiresAt), Expires: sub.ExpiresAt, Left: d.Words.Countdown(left),
			Pct: validityPct(sub.StartedAt, sub.ExpiresAt, now)}
		if sub.PendingStart == 1 {
			d.Plan.Pending, d.Plan.Left, d.Plan.Pct = true, s.T("Starts on first login"), 0
		}
		d.Period = pTotal{Down: view.HumanBytes(sub.UsedOutOctets), Up: view.HumanBytes(sub.UsedInOctets)}
		d.Usage = s.pUsage(ctx, *c, *sub, p, d.Words, sub.UsedInOctets+sub.UsedOutOctets)
	} else {
		d.Usage.Note = s.T("No active plan")
		if len(subs) > 0 { // only finished plans: say which one ended
			p, _ := s.Queries.GetPlan(ctx, subs[0].PlanID)
			d.PlanEnded = fmt.Sprintf(s.T("Plan %s ended on %s"), p.Name, s.TS(subs[0].ExpiresAt))
		}
		canExtend := (st["extend_expired"] == "1" || st["extend_expired"] == "yes") && c.Status == "Active"
		for _, x := range subs { // newest first: the last plan that is not active
			if x.Status != "active" {
				if p, _ := s.Queries.GetPlan(ctx, x.PlanID); canExtend && billing.SellablePlan(p) {
					d.Extend = &pExtend{ID: x.ID, Plan: p.Name}
				}
				break
			}
		}
	}

	if d.Live, err = s.pLiveOf(ctx, *c, now, d.Words); err != nil {
		s.Fail(w, "portal connection", err)
		return
	}
	// the chart: the last 7 days; today is the last bar
	rows, err := s.Queries.ListRadiusUsageDaily(ctx, db.ListRadiusUsageDailyParams{Name1: n1, Name2: n2, Since: core.WeekSince(now, s.Location())})
	if err != nil {
		s.Fail(w, "portal usage chart", err)
		return
	}
	totals := make([]core.DayTotal, len(rows))
	for i, x := range rows {
		totals[i] = core.DayTotal{Day: x.Day, Input: x.Input, Output: x.Output}
	}
	days := core.DailyUsage(totals, 7, now, s.Location())
	d.Days = core.ChartBars(days)
	d.Today = pTotal{Down: view.HumanBytes(days[len(days)-1].Down), Up: view.HumanBytes(days[len(days)-1].Up)}

	d.Trx, err = s.Queries.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 3})
	if err != nil {
		s.Fail(w, "portal dashboard orders", err)
		return
	}
	d.Pay = s.pendingPay(ctx, c.ID)
	if d.Outage, err = billing.MassDropNow(ctx, s.Queries, st, c.Username, time.Unix(now, 0)); err != nil {
		s.Fail(w, "portal outage", err)
		return
	}
	s.Prender(w, r, code, "p_dashboard", core.Page{Title: "Dashboard", Error: errMsg, Data: d})
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
func (s *Handlers) pUsage(ctx context.Context, c db.Customer, sub db.Subscription, p db.Plan, w core.PWords, used int64) pUsage {
	if p.Limited == 1 && p.Device != "Radius" {
		return pUsage{Note: s.T("Usage is not recorded for this plan")}
	}
	q := diagnose.PlanQuota(sub, p)
	u := pUsage{}
	u.TotalNum, u.TotalUnit = view.HumanBytesParts(used)
	if q.HasData {
		u.Data = meter(q.Data, q.DataLim, view.HumanBytes)
		u.LeftNum, u.LeftUnit = view.HumanBytesParts(max(q.DataLim-q.Data, 0))
		u.Limit = view.HumanBytes(q.DataLim)
	}
	if q.HasTime {
		u.HasTime = true
		u.Time = meter(q.Time, q.TimeLim, w.Span)
	}
	return u
}

// meter fills a bar: used over limit, warn above 80%.
func meter(used, lim int64, text func(int64) string) pMeter {
	pct := min(used*100/lim, 100)
	return pMeter{Text: text(used) + " / " + text(lim), Bar: true, Pct: int(pct), Warn: used*100 > lim*80}
}

// pSession maps one RADIUS session to a history row; the duration of an open session runs to now.
func (s *Handlers) pSession(x db.RadiusSession, now int64, w core.PWords) pSess {
	end := now
	if x.StoppedAt.Valid {
		end = x.StoppedAt.Int64
	}
	return pSess{Start: s.TS(x.StartedAt), For: w.Span(end - x.StartedAt), Up: view.HumanBytes(x.InputOctets), Down: view.HumanBytes(x.OutputOctets),
		MAC: maskMAC(x.Mac), Open: !x.StoppedAt.Valid && now-x.UpdatedAt <= radius.StaleFor(x.NasIp)}
}

// pOrders lists the customer's own transactions, view.PerPage at a time.
func (s *Handlers) pOrders(w http.ResponseWriter, r *http.Request) {
	_, pg := view.Paging(r)
	cust := sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}
	trx, err := s.Queries.ListTransactionsByCustomer(r.Context(), db.ListTransactionsByCustomerParams{CustomerID: cust, Limit: pg.Limit(), Offset: pg.Offset()})
	if err != nil {
		s.Fail(w, "portal orders", err)
		return
	}
	total, err := s.Queries.CountTransactionsByCustomer(r.Context(), cust)
	if err != nil {
		s.Fail(w, "count portal orders", err)
		return
	}
	data := pageOf(trx, pg, total, "/portal/orders")
	data.Pay = s.pendingPay(r.Context(), customerFrom(r).ID)
	s.Prender(w, r, 200, "p_orders", core.Page{Title: "Order History", Data: data})
}

// pInvoice shows the invoice only for the customer's own transaction.
func (s *Handlers) pInvoice(w http.ResponseWriter, r *http.Request) {
	t, err := s.Queries.GetTransaction(r.Context(), httpx.PathID(r))
	if err != nil || !t.CustomerID.Valid || t.CustomerID.Int64 != customerFrom(r).ID {
		http.NotFound(w, r)
		return
	}
	s.Invoice(w, r, t)
}
