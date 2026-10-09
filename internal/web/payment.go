package web

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

// Gateway is what the web layer needs from an online gateway (Tripay today).
type Gateway interface {
	payment.PaymentGateway
	Channels(ctx context.Context) ([]payment.Channel, error)
}

// paymentCache holds the active channel list for channelTTL.
// ponytail: single-gateway cache, not cleared when the keys change (expires in 10 min).
type paymentCache struct {
	mu   sync.Mutex
	at   time.Time
	list []payment.Channel
}

const (
	channelTTL = 10 * time.Minute
	payExpiry  = 24 * time.Hour
)

// gateway builds the configured gateway from the tripay_* settings; nil when none is selected.
func (s *Server) gateway(st map[string]string) (Gateway, error) {
	if st["payment_gateway"] != "tripay" {
		return nil, nil
	}
	cfg := map[string]string{"api_key": st["tripay_api_key"], "private_key": st["tripay_private_key"],
		"merchant_code": st["tripay_merchant_code"], "mode": st["tripay_mode"], "channel": st["tripay_channel"]}
	if s.NewGateway != nil {
		return s.NewGateway(cfg)
	}
	return payment.NewTripay(cfg)
}

// activeChannels returns the usable channels, cached.
func (s *Server) activeChannels(ctx context.Context, g Gateway) ([]payment.Channel, error) {
	c := &s.chanCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > channelTTL || c.list == nil {
		l, err := g.Channels(ctx)
		if err != nil {
			return nil, err
		}
		c.list, c.at = nil, time.Now()
		for _, ch := range l {
			if ch.Active {
				c.list = append(c.list, ch)
			}
		}
	}
	return c.list, nil
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) paymentRoutes(mux *http.ServeMux) {
	mux.Handle("POST /portal/plans/{id}/pay", s.requireCustomer(s.pPay))
	mux.Handle("POST /portal/topup", s.requireCustomer(s.pTopUp))
	mux.Handle("GET /portal/payments/{id}", s.requireCustomer(s.pPayView))
	mux.Handle("POST /portal/payments/{id}/check", s.requireCustomer(s.pPayCheck))
	mux.HandleFunc("POST /callback/tripay", s.tripayCallback)
}

// pPay is the gateway branch of the order: pending row, gateway transaction, redirect to pay_url.
// Coupons follow the old flow: validated now against the plan price, claimed when paid.
func (s *Server) pPay(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal pay", err)
		return
	}
	g, err := s.gateway(st)
	if err != nil || g == nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.queries.GetPlan(r.Context(), pathID(r))
	if errors.Is(err, sql.ErrNoRows) || err == nil && (p.Enabled != 1 || p.Type == "Balance" || p.Billing != "prepaid") {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "portal pay", err)
		return
	}
	if c.Status != "Active" { // recharge would be refused after payment (PHP Package.php:44)
		s.plansPage(w, r, 200, "account is not active")
		return
	}
	chans, err := s.activeChannels(r.Context(), g)
	channel, ok := r.PostFormValue("channel"), false
	for _, ch := range chans {
		ok = ok || ch.Code == channel
	}
	if err != nil || !ok {
		slog.Warn("pay: channel", "channel", channel, "err", err)
		s.plansPage(w, r, 200, "Please select Payment Gateway")
		return
	}
	amount, code := p.Price, strings.TrimSpace(r.PostFormValue("coupon"))
	if code != "" {
		cp, err := s.queries.GetCouponByCode(r.Context(), code)
		if errors.Is(err, sql.ErrNoRows) {
			err = billing.ErrCouponNotFound
		} else if err == nil {
			amount, err = billing.Discount(cp, p.Price, time.Now().In(s.location()).Format("2006-01-02"))
			code = cp.Code
		}
		if err != nil {
			if m := couponErr(err); m != "" {
				s.plansPage(w, r, 200, s.catalog.T(s.language(), m))
				return
			}
			s.fail(w, "portal pay coupon", err)
			return
		}
	}
	s.payOrder(w, r, g, c, p.ID, p.Name, amount, code, channel)
}

// pTopUp is allow_balance_custom (only with a gateway): a custom amount paid online is added to the balance.
func (s *Server) pTopUp(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal topup", err)
		return
	}
	g, err := s.gateway(st)
	if err != nil || g == nil || st["allow_balance_custom"] != "yes" || st["enable_balance"] == "no" {
		http.NotFound(w, r)
		return
	}
	amount, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("amount")), 10, 64)
	chans, err := s.activeChannels(r.Context(), g)
	channel, ok := r.PostFormValue("channel"), false
	for _, ch := range chans {
		ok = ok || ch.Code == channel
	}
	switch {
	case amount <= 0 || amount > 1_000_000_000:
		s.plansPage(w, r, 200, "Please enter amount")
	case err != nil || !ok:
		s.plansPage(w, r, 200, "Please select Payment Gateway")
	default:
		s.payOrder(w, r, g, c, 0, "Custom Balance", amount, "", channel)
	}
}

// payOrder stores the pending payment, creates the gateway transaction and redirects to its pay URL.
// planID 0 = custom balance top-up.
func (s *Server) payOrder(w http.ResponseWriter, r *http.Request, g Gateway, c *db.Customer, planID int64, planName string, amount int64, code, channel string) {
	raw := make([]byte, 8)
	rand.Read(raw)
	exp := time.Now().Add(payExpiry)
	pr, err := s.queries.CreatePaymentRequest(r.Context(), db.CreatePaymentRequestParams{Ref: "NB" + strings.ToUpper(hex.EncodeToString(raw)),
		Gateway: g.Name(), CustomerID: c.ID, PlanID: planID, Amount: amount, Coupon: code, Channel: channel, ExpiresAt: exp.Unix()})
	if err != nil {
		s.fail(w, "portal pay", err)
		return
	}
	cr, err := g.CreateTransaction(r.Context(), payment.Transaction{ID: pr.Ref, Amount: amount, PlanName: planName, Name: c.Fullname,
		Email: c.Email, Phone: c.Phone, ReturnURL: baseURL(r) + "/portal/payments/" + strconv.FormatInt(pr.ID, 10), Channel: channel, Expiry: exp})
	if err != nil {
		slog.Error("pay: create transaction", "ref", pr.Ref, "err", err)
		s.queries.ClosePaymentRequest(r.Context(), db.ClosePaymentRequestParams{Status: "failed", Ref: pr.Ref})
		s.plansPage(w, r, http.StatusBadGateway, "Payment gateway error, try again later")
		return
	}
	if err := s.queries.SetPaymentGatewayRef(r.Context(), db.SetPaymentGatewayRefParams{GatewayRef: cr.Reference, PayUrl: cr.PayURL, ID: pr.ID}); err != nil {
		s.fail(w, "portal pay", err)
		return
	}
	http.Redirect(w, r, cr.PayURL, http.StatusSeeOther)
}

// ownPayment loads the payment of the logged-in customer (404 for anyone else's).
func (s *Server) ownPayment(w http.ResponseWriter, r *http.Request) (db.PaymentRequest, bool) {
	pr, err := s.queries.GetPaymentRequest(r.Context(), pathID(r))
	if err != nil || pr.CustomerID != customerFrom(r).ID {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Error("load payment", "err", err)
		}
		http.NotFound(w, r)
		return pr, false
	}
	return pr, true
}

func (s *Server) pPayView(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.ownPayment(w, r)
	if !ok {
		return
	}
	pl, _ := s.queries.GetPlan(r.Context(), pr.PlanID)
	if pr.PlanID == 0 {
		pl.Name = "Custom Balance"
	}
	s.prender(w, r, 200, "p_payment", Page{Title: "Order Details", Data: struct {
		P    db.PaymentRequest
		Plan string
	}{pr, pl.Name}})
}

// pPayCheck asks the gateway for the status, for when the callback did not arrive.
func (s *Server) pPayCheck(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.ownPayment(w, r)
	if !ok {
		return
	}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal pay check", err)
		return
	}
	g, err := s.gateway(st)
	if err != nil || g == nil {
		http.NotFound(w, r)
		return
	}
	back := "/portal/payments/" + strconv.FormatInt(pr.ID, 10)
	if pr.Status == "pending" && pr.GatewayRef != "" {
		got, err := g.Status(r.Context(), payment.Transaction{ID: pr.Ref, Reference: pr.GatewayRef})
		if err != nil {
			slog.Error("pay: status", "ref", pr.Ref, "err", err)
			s.flashTo(w, r, back, "Payment gateway error, try again later")
			return
		}
		if err := s.settlePayment(r.Context(), pr, got); err != nil {
			s.fail(w, "portal pay check", err)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// settlePayment applies a gateway status. Paid claims the row and recharges in one transaction,
// so it happens at most once however often the callback and "check status" fire.
func (s *Server) settlePayment(ctx context.Context, pr db.PaymentRequest, st payment.Status) error {
	switch st {
	case payment.Paid:
		if s.Billing == nil {
			return errors.New("billing not configured")
		}
		if pr.Status == "expired" || pr.Status == "failed" {
			slog.Warn("payment paid after request was closed", "ref", pr.Ref)
		}
		claim := func(q *db.Queries) (bool, error) {
			n, err := q.ClaimPaymentPaid(ctx, pr.Ref)
			return n > 0, err
		}
		if pr.PlanID == 0 { // custom balance top-up
			return s.Billing.TopUpPaid(ctx, claim, pr.CustomerID, pr.Amount, "Tripay - "+pr.Channel)
		}
		return s.Billing.RechargePaid(ctx, claim, pr.CustomerID, pr.PlanID, "Tripay - "+pr.Channel, pr.Coupon, pr.Amount)
	case payment.Failed, payment.Expired:
		_, err := s.queries.ClosePaymentRequest(ctx, db.ClosePaymentRequestParams{Status: string(st), Ref: pr.Ref})
		return err
	}
	return nil
}

// tripayCallback is the server-to-server notification. CSRF-exempt (see Handler); the HMAC
// signature is the authentication. A valid signature always gets {"success":true}, so the
// gateway stops retrying even for unknown or already-handled references.
func (s *Server) tripayCallback(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "tripay callback", err)
		return
	}
	g, err := s.gateway(st)
	if err != nil || g == nil {
		http.NotFound(w, r)
		return
	}
	ref, status, err := g.HandleCallback(r)
	if err != nil {
		slog.Warn("tripay callback rejected", "ip", clientIP(r), "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	pr, err := s.queries.GetPaymentRequestByRef(r.Context(), ref)
	if err != nil {
		slog.Warn("tripay callback: unknown reference", "ref", ref, "err", err)
	} else if err := s.settlePayment(r.Context(), pr, status); err != nil {
		s.fail(w, "tripay callback settle "+ref, err) // 500 makes Tripay retry
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ---- admin (A71-A73) ----

func (s *Server) paymentAdminRoutes(mux *http.ServeMux, managers func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/payment-gateway", managers(http.HandlerFunc(s.pgList)))
	mux.Handle("GET /admin/payment-gateway/audit", managers(http.HandlerFunc(s.pgAudit)))
	mux.Handle("GET /admin/payment-gateway/audit/export", managers(http.HandlerFunc(s.pgAuditExport)))
	mux.Handle("GET /admin/payment-gateway/audit/{id}", managers(http.HandlerFunc(s.pgAuditView)))
}

func (s *Server) pgList(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "payment gateways", err)
		return
	}
	state := "Disabled"
	if st["payment_gateway"] == "tripay" {
		state = "Enabled"
	}
	lp := listPage{Heading: "Payment Gateway", Base: "/admin/payment-gateway", Cols: []string{"Gateway", "Status"},
		Rows:  []listRow{{1, []string{"Tripay", state}}},
		Links: []option{{"/admin/settings/payment", "Settings"}, {"/admin/payment-gateway/audit", "Payment Audit"}}}
	s.renderList(w, r, lp)
}

var pgAuditCols = []string{"Transaction ID", "Gateway Ref", "Username", "Package Name", "Package Price", "Channel", "Status", "Created On", "Expires On", "Paid Date"}

func (s *Server) pgAuditParams(r *http.Request) db.SearchPaymentRequestsParams {
	_, _, f, t := s.dateRange(r)
	return db.SearchPaymentRequestsParams{Q: strings.TrimSpace(r.URL.Query().Get("q")), Status: r.URL.Query().Get("status"), FromTs: f, ToTs: t}
}

func (s *Server) pgAuditRows(rs []db.SearchPaymentRequestsRow) [][]string {
	out := [][]string{}
	for _, x := range rs {
		paid := ""
		if x.PaidAt.Valid {
			paid = s.ts(x.PaidAt.Int64)
		}
		out = append(out, []string{x.Ref, x.GatewayRef, x.Username, x.PlanName, money(x.Amount), x.Channel, x.Status, s.ts(x.CreatedAt), s.ts(x.ExpiresAt), paid})
	}
	return out
}

var pgStatuses = []option{{"", "Status"}, {"pending", "pending"}, {"paid", "paid"}, {"failed", "failed"}, {"expired", "expired"}}

func (s *Server) pgAudit(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	p := s.pgAuditParams(r)
	p.PageLimit, p.PageOffset = limit, off
	rs, err := s.queries.SearchPaymentRequests(r.Context(), p)
	if err != nil {
		s.fail(w, "payment audit", err)
		return
	}
	from, to, _, _ := s.dateRange(r)
	lp := listPage{Heading: "Payment Audit", Base: "/admin/payment-gateway/audit", Q: q, Searchable: true, Dates: true, From: from, To: to,
		Cols: pgAuditCols, ViewLink: true, Filters: []filter{{"status", p.Status, pgStatuses}}}
	for i, c := range s.pgAuditRows(rs) {
		lp.Rows = append(lp.Rows, listRow{rs[i].ID, c})
	}
	lp.Links = []option{{"/admin/payment-gateway/audit/export?" + lp.query().Encode(), "Export CSV"}}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) pgAuditExport(w http.ResponseWriter, r *http.Request) {
	p := s.pgAuditParams(r)
	p.PageLimit = -1
	rs, err := s.queries.SearchPaymentRequests(r.Context(), p)
	if err != nil {
		s.fail(w, "export payment audit", err)
		return
	}
	s.writeCSV(w, "payment-audit", pgAuditCols, s.pgAuditRows(rs))
}

func (s *Server) pgAuditView(w http.ResponseWriter, r *http.Request) {
	x, err := s.queries.GetPaymentAudit(r.Context(), pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	paid := ""
	if x.PaidAt.Valid {
		paid = s.ts(x.PaidAt.Int64)
	}
	rows := [][2]string{{"Transaction ID", x.Ref}, {"Gateway Ref", x.GatewayRef}, {"Username", x.Username}, {"Package Name", x.PlanName},
		{"Package Price", money(x.Amount)}, {"Coupon Code", x.Coupon}, {"Channel", x.Channel}, {"Payment Link", x.PayUrl}, {"Status", x.Status},
		{"Created On", s.ts(x.CreatedAt)}, {"Expires On", s.ts(x.ExpiresAt)}, {"Paid Date", paid}}
	s.render(w, r, 200, "pay_audit", Page{Title: "Payment Audit", Data: rows})
}
