package web

// Customer portal dashboard, orders, invoices and activations.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
)

// ---- portal ----

// pTrxPage is one page of the customer's orders or activations.
type pTrxPage struct {
	Rows       []db.Transaction
	Prev, Next int // page numbers, 0 = none
}

// pageOf drops the extra row paging() asks for and sets the neighbour pages.
func pageOf(trx []db.Transaction, page int) pTrxPage {
	d := pTrxPage{Rows: trx}
	if len(trx) > perPage {
		d.Rows = trx[:perPage]
		d.Next = page + 1
	}
	if page > 1 {
		d.Prev = page - 1
	}
	return d
}

// pActivations lists the customer's own plan purchases and activations, perPage at a time.
func (s *Server) pActivations(w http.ResponseWriter, r *http.Request) {
	_, page, limit, off := paging(r)
	trx, err := s.queries.ListActivationsByCustomer(r.Context(), db.ListActivationsByCustomerParams{CustomerID: sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}, Limit: limit, Offset: off})
	if err != nil {
		s.fail(w, "portal activations", err)
		return
	}
	s.prender(w, r, 200, "p_activation", Page{Title: "Activation History", Data: pageOf(trx, page)})
}

// ---- dashboard, orders ----

const sessPerPage = 10 // connection history rows per dashboard page

// pPlan is the active plan card. Left is the countdown as text; the Alpine ticker keeps it live.
type pPlan struct {
	Name    string
	Ends    string // expiry date and time
	Expires int64
	Left    string
}

// pExtend is the renew button of an ended plan (POST /portal/extend/{ID}).
type pExtend struct {
	ID   int64
	Plan string
}

// pMeter is one usage row: a bar when the plan has a limit, plain text ("Tanpa batas") when not.
type pMeter struct {
	Text string
	Bar  bool
	Pct  int  // bar fill, 0..100
	Warn bool // above 80% of the limit
}

type pUsage struct {
	Note    string // why there are no meters: no plan, or the plan's usage is not tracked here
	Data    pMeter
	HasTime bool // the plan has a time limit
	Time    pMeter
}

// pConn is the connection card: online now (open RADIUS session), else last seen.
type pConn struct {
	Online bool
	Has    bool   // some session is on record
	Seen   string // offline: last time the device was seen
	IP     string
	MAC    string // masked
	Since  string
	For    string
}

// pSess is one row of the connection history.
type pSess struct {
	Start, For, Up, Down, MAC string
	Open                      bool
}

type pDashData struct {
	Plan         *pPlan   // nil when there is no active plan
	PlanEnded    string   // no active plan: the last plan that ended, "" if none
	Extend       *pExtend // no active plan and extend_expired is on: renew the last ended plan
	Usage        pUsage
	Conn         pConn
	Sessions     []pSess
	SPrev, SNext int // connection history pages (?spage=), 0 = none
	Words        pWords
	Now          int64
	LastLogin    string
	Notice       string // the announcement page, plain text
	Transfer     bool   // allow_balance_transfer
	Minimum      string
	Voucher      bool
	Company      string // operator contact from Settings
	Phone        string
	WA           string           // wa.me link of Phone, "" when it is not a usable number
	Trx          []db.Transaction // latest 10 orders, "See all" opens /portal/orders
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

// span: "3 hari 4 jam", "2 jam 15 menit", "45 menit". The ticker's gbSpan in dash_cards.html does the same.
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

// pageParam reads a 1-based page number from the query string; bad or missing is page 1.
func pageParam(r *http.Request, key string) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, 100000)
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
	d := pDashData{Words: s.pTexts(), Now: now, Transfer: st["enable_balance"] != "no" && st["allow_balance_transfer"] == "yes", Minimum: st["minimum_transfer"], Voucher: st["disable_voucher"] != "yes",
		Company: st["company_name"], Phone: st["phone"], WA: waLink(st["phone"], st["country_code_phone"])}
	if c.LastLoginAt.Valid {
		d.LastLogin = s.ts(c.LastLoginAt.Int64)
	}

	// the active plan: the one that ends last
	var sub *db.Subscription
	for i := range subs {
		if subs[i].Status == "active" && subs[i].ExpiresAt > now && (sub == nil || subs[i].ExpiresAt > sub.ExpiresAt) {
			sub = &subs[i]
		}
	}
	if sub != nil {
		p, _ := s.queries.GetPlan(ctx, sub.PlanID)
		left := sub.ExpiresAt - now
		d.Plan = &pPlan{Name: p.Name, Ends: s.ts(sub.ExpiresAt), Expires: sub.ExpiresAt, Left: d.Words.countdown(left)}
		d.Usage = s.pUsage(ctx, names, *sub, p, d.Words)
	} else {
		d.Usage.Note = s.catalog.T(s.language(), "No active plan")
		if len(subs) > 0 { // only finished plans: say which one ended
			p, _ := s.queries.GetPlan(ctx, subs[0].PlanID)
			d.PlanEnded = fmt.Sprintf(s.catalog.T(s.language(), "Plan %s ended on %s"), p.Name, s.ts(subs[0].ExpiresAt))
		}
		canExtend := (st["extend_expired"] == "1" || st["extend_expired"] == "yes") && c.Status == "Active"
		for _, x := range subs { // newest first: the last plan that is not active
			if x.Status != "active" {
				if p, _ := s.queries.GetPlan(ctx, x.PlanID); canExtend {
					d.Extend = &pExtend{ID: x.ID, Plan: p.Name}
				}
				break
			}
		}
	}

	if d.Conn, err = s.pConnection(ctx, names, now, d.Words); err != nil {
		s.fail(w, "portal connection", err)
		return
	}
	if d.Sessions, d.SPrev, d.SNext, err = s.pSessions(ctx, names, pageParam(r, "spage"), now, d.Words); err != nil {
		s.fail(w, "portal sessions", err)
		return
	}

	d.Trx, err = s.queries.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 10})
	if err != nil {
		s.fail(w, "portal dashboard orders", err)
		return
	}
	ann, err := s.queries.GetPage(ctx, "announcement")
	if err != nil {
		s.fail(w, "portal announcement", err)
		return
	}
	d.Notice = ann.Body
	s.prender(w, r, code, "p_dashboard", Page{Title: "Dashboard", Error: errMsg, Data: d})
}

// pUsage: data and time used since the subscription started, the same sums the RADIUS login check uses.
func (s *Server) pUsage(ctx context.Context, names []string, sub db.Subscription, p db.Plan, w pWords) pUsage {
	if p.Limited == 1 && p.Device != "Radius" {
		return pUsage{Note: s.catalog.T(s.language(), "Usage is not recorded for this plan")}
	}
	q := s.planQuota(ctx, names, sub, p)
	u := pUsage{Data: pMeter{Text: s.catalog.T(s.language(), "No limit")}}
	if q.HasData {
		u.Data = meter(q.Data, q.DataLim, humanBytes)
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

// pConnection: online when the customer has an open session that was updated recently, else last seen.
func (s *Server) pConnection(ctx context.Context, names []string, now int64, w pWords) (pConn, error) {
	var open []db.RadiusSession
	var last *db.RadiusSession
	for _, n := range names {
		ss, err := s.queries.ListOpenRadiusSessionsByUser(ctx, n)
		if err != nil {
			return pConn{}, err
		}
		open = append(open, ss...)
		recent, err := s.queries.ListRecentRadiusSessionsByUser(ctx, n)
		if err != nil {
			return pConn{}, err
		}
		if len(recent) > 0 && (last == nil || recent[0].StartedAt > last.StartedAt) {
			last = &recent[0]
		}
	}
	var cur *db.RadiusSession
	for i := range open {
		if cur == nil || open[i].UpdatedAt > cur.UpdatedAt {
			cur = &open[i]
		}
	}
	var c pConn
	if last != nil {
		c.Has = true
		c.Seen = s.ts(max(last.UpdatedAt, last.StoppedAt.Int64))
	}
	if cur != nil && now-cur.UpdatedAt <= radius.StaleAfter {
		c.Has, c.Online = true, true
		c.IP, c.MAC = cur.FramedIp, maskMAC(cur.Mac)
		c.Since, c.For = s.ts(cur.StartedAt), w.span(now-cur.StartedAt)
	}
	return c, nil
}

// pSessions is one page of the customer's own connection history, newest first.
func (s *Server) pSessions(ctx context.Context, names []string, page int, now int64, w pWords) ([]pSess, int, int, error) {
	ss, err := s.queries.ListRadiusSessionsByUsers(ctx, db.ListRadiusSessionsByUsersParams{Name1: names[0], Name2: names[len(names)-1],
		PageLimit: sessPerPage + 1, PageOffset: int64(page-1) * sessPerPage})
	if err != nil {
		return nil, 0, 0, err
	}
	prev, next := 0, 0
	if len(ss) > sessPerPage {
		ss, next = ss[:sessPerPage], page+1
	}
	if page > 1 {
		prev = page - 1
	}
	out := make([]pSess, 0, len(ss))
	for _, x := range ss {
		end := now
		if x.StoppedAt.Valid {
			end = x.StoppedAt.Int64
		}
		out = append(out, pSess{Start: s.ts(x.StartedAt), For: w.span(end - x.StartedAt), Up: humanBytes(x.InputOctets), Down: humanBytes(x.OutputOctets),
			MAC: maskMAC(x.Mac), Open: !x.StoppedAt.Valid && now-x.UpdatedAt <= radius.StaleAfter})
	}
	return out, prev, next, nil
}

// pOrders lists the customer's own transactions, perPage at a time.
func (s *Server) pOrders(w http.ResponseWriter, r *http.Request) {
	_, page, limit, off := paging(r)
	trx, err := s.queries.ListTransactionsByCustomer(r.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}, Limit: limit, Offset: off})
	if err != nil {
		s.fail(w, "portal orders", err)
		return
	}
	s.prender(w, r, 200, "p_orders", Page{Title: "Order History", Data: pageOf(trx, page)})
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

type radiusUsage struct {
	Total string
	Rows  []sessRow
}

// radiusUsage: bytes since the active subscription started plus the last 10 sessions.
func (s *Server) radiusUsage(ctx context.Context, c db.Customer) *radiusUsage {
	u := &radiusUsage{}
	var total int64
	if p, err := s.queries.GetRadiusPlan(ctx, c.ID); err == nil {
		total, _ = s.queries.SumRadiusUsage(ctx, db.SumRadiusUsageParams{Username: c.Username, StartedAt: p.StartedAt})
	}
	u.Total = humanBytes(total)
	ss, _ := s.queries.ListRecentRadiusSessionsByUser(ctx, c.Username)
	u.Rows = s.sessRows(ss, time.Now().Unix())
	return u
}
