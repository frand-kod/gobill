package web

// Customer portal dashboard, orders, invoices and activations.

import (
	"database/sql"
	"net/http"

	"context"
	"github.com/frand-kod/gobill/internal/db"
	"time"
)

// ---- portal ----

func (s *Server) pActivations(w http.ResponseWriter, r *http.Request) {
	trx, err := s.queries.ListTransactionsByCustomer(r.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}, Limit: 200})
	if err != nil {
		s.fail(w, "portal activations", err)
		return
	}
	var out []db.Transaction
	for _, t := range trx {
		if t.Type != "Balance" { // balance moves are not activations
			out = append(out, t)
		}
	}
	s.prender(w, r, 200, "p_activation", Page{Title: "Activation History", Data: out})
}

// ---- dashboard, orders ----

type pSubRow struct {
	ID                 int64
	Plan, Type, Status string
	Expires            int64
	CanExtend          bool
}

type pDashData struct {
	Subs     []pSubRow
	Notice   string // the announcement page, plain text
	Transfer bool   // allow_balance_transfer
	Minimum  string
	Voucher  bool
	Company  string // operator contact from Settings
	Phone    string
	WA       string           // wa.me link of Phone, "" when it is not a usable number
	Trx      []db.Transaction // latest 10 orders, "See all" opens /portal/orders
}

// pOrdersData is one page of the order history.
type pOrdersData struct {
	Rows       []db.Transaction
	Prev, Next int // page numbers, 0 = none
}

func (s *Server) pDashboard(w http.ResponseWriter, r *http.Request) { s.pDash(w, r, 200, "") }

func (s *Server) pDash(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	c := customerFrom(r)
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal dashboard", err)
		return
	}
	subs, err := s.queries.ListSubscriptionsByCustomer(r.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
	if err != nil {
		s.fail(w, "portal dashboard", err)
		return
	}
	d := pDashData{Transfer: st["enable_balance"] != "no" && st["allow_balance_transfer"] == "yes", Minimum: st["minimum_transfer"], Voucher: st["disable_voucher"] != "yes",
		Company: st["company_name"], Phone: st["phone"], WA: waLink(st["phone"], st["country_code_phone"])}
	canExtend := (st["extend_expired"] == "1" || st["extend_expired"] == "yes") && c.Status == "Active"
	for _, sub := range subs {
		p, _ := s.queries.GetPlan(r.Context(), sub.PlanID)
		d.Subs = append(d.Subs, pSubRow{sub.ID, p.Name, sub.Type, sub.Status, sub.ExpiresAt, canExtend && sub.Status != "active"})
	}
	d.Trx, err = s.queries.ListTransactionsByCustomer(r.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 10})
	if err != nil {
		s.fail(w, "portal dashboard orders", err)
		return
	}
	ann, err := s.queries.GetPage(r.Context(), "announcement")
	if err != nil {
		s.fail(w, "portal announcement", err)
		return
	}
	d.Notice = ann.Body
	s.prender(w, r, code, "p_dashboard", Page{Title: "Dashboard", Error: errMsg, Data: d})
}

// pOrders lists the customer's own transactions, perPage at a time.
func (s *Server) pOrders(w http.ResponseWriter, r *http.Request) {
	_, page, limit, off := paging(r)
	trx, err := s.queries.ListTransactionsByCustomer(r.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}, Limit: limit, Offset: off})
	if err != nil {
		s.fail(w, "portal orders", err)
		return
	}
	d := pOrdersData{Rows: trx}
	if len(trx) > perPage {
		d.Rows = trx[:perPage]
		d.Next = page + 1
	}
	if page > 1 {
		d.Prev = page - 1
	}
	s.prender(w, r, 200, "p_orders", Page{Title: "Order History", Data: d})
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
