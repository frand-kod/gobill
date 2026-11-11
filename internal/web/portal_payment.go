package web

// Customer payment checkout and status check.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"log/slog"
	"net/http"

	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
	"strconv"
	"strings"
	"time"
)

// pPay is the gateway branch of the order: pending row, gateway transaction, redirect to pay_url.
// Coupons are validated against the plan price and reserved when the order is created (payOrder).
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
	p, err := s.queries.GetPlan(r.Context(), httpx.PathID(r))
	if errors.Is(err, sql.ErrNoRows) || err == nil && (p.Enabled != 1 || p.Type == "Balance" || p.Billing != "prepaid") {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "portal pay", err)
		return
	}
	if !billing.SellablePlan(p) { // admin-only or Rp 0: no order is created
		s.plansPage(w, r, 200, billing.ErrAdminOnlyPlan.Error())
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
		s.plansPage(w, r, 200, "Please choose how to pay")
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
				s.plansPage(w, r, 200, s.t(m))
				return
			}
			s.fail(w, "portal pay coupon", err)
			return
		}
	}
	if s.Billing != nil { // same formula as recharge: tax on the (discounted) price, then the customer's bills
		if amount, err = s.Billing.OrderPrice(r.Context(), c.ID, amount); err != nil {
			s.fail(w, "portal pay price", err)
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
		s.plansPage(w, r, 200, "Please choose how to pay")
	default:
		s.payOrder(w, r, g, c, 0, "Custom Balance", amount, "", channel)
	}
}

// payOrder stores the pending payment, creates the gateway transaction and redirects to its pay URL.
// planID 0 = custom balance top-up.
func (s *Server) payOrder(w http.ResponseWriter, r *http.Request, g Gateway, c *db.Customer, planID int64, planName string, amount int64, code, channel string) {
	if code != "" { // reserve the coupon use now; refused when it ran out since the price was shown
		if err := billing.ReserveCoupon(r.Context(), s.queries, code); err != nil {
			if m := couponErr(err); m != "" {
				s.plansPage(w, r, 200, s.t(m))
				return
			}
			s.fail(w, "portal pay coupon", err)
			return
		}
	}
	raw := make([]byte, 8)
	rand.Read(raw)
	exp := time.Now().Add(payExpiry)
	pr, err := s.queries.CreatePaymentRequest(r.Context(), db.CreatePaymentRequestParams{Ref: "NB" + strings.ToUpper(hex.EncodeToString(raw)),
		Gateway: g.Name(), CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Username: c.Username, PlanID: planID, Amount: amount, Coupon: code, Channel: channel, ExpiresAt: exp.Unix()})
	if err != nil {
		if code != "" {
			s.queries.ReleaseCoupon(r.Context(), code)
		}
		s.fail(w, "portal pay", err)
		return
	}
	base := baseURL(r)
	if st, err := s.loadSettings(r.Context()); err == nil && st["app_url"] != "" {
		base = strings.TrimRight(st["app_url"], "/")
	}
	cr, err := g.CreateTransaction(r.Context(), payment.Transaction{ID: pr.Ref, Amount: amount, PlanName: planName, Name: c.Fullname,
		Email: c.Email, Phone: c.Phone, ReturnURL: baseURL(r) + "/portal/payments/" + strconv.FormatInt(pr.ID, 10), CallbackURL: base + "/callback/" + g.Name(), Channel: channel, Expiry: exp})
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
	pr, err := s.queries.GetPaymentRequest(r.Context(), httpx.PathID(r))
	if err != nil || !pr.CustomerID.Valid || pr.CustomerID.Int64 != customerFrom(r).ID { // NULL = customer deleted: not yours
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
		P       db.PaymentRequest
		Plan    string
		Channel string
	}{pr, pl.Name, s.channelName(pr.Channel)}})
}

// pendingPay is the customer's newest unpaid payment that has not expired, nil when there is none.
func (s *Server) pendingPay(ctx context.Context, customerID int64) *db.PaymentRequest {
	pr, err := s.queries.GetPendingPaymentRequestByCustomer(ctx, db.GetPendingPaymentRequestByCustomerParams{CustomerID: sql.NullInt64{Int64: customerID, Valid: true}, ExpiresAt: time.Now().Unix()})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("pending payment", "err", err)
		}
		return nil
	}
	return &pr
}

// channelName is the gateway's name for a channel code, from the cached list; the code itself when unknown.
func (s *Server) channelName(code string) string {
	s.chanCache.mu.Lock()
	defer s.chanCache.mu.Unlock()
	for _, ch := range s.chanCache.list {
		if ch.Code == code && ch.Name != "" {
			return ch.Name
		}
	}
	return code
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
		if wait := s.payCheckWait(pr.ID); wait > 0 {
			s.sessions.Put(r.Context(), "flash", fmt.Sprintf(s.t("Wait %d seconds before checking again"), wait))
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
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

// payCheckGap is the minimum time between gateway status calls for one payment (Duitku blocks the
// merchant for about an hour on excess status calls).
const payCheckGap = 30 * time.Second

// payCheckWait records a status call for payment id. It returns 0 when the call may go ahead, otherwise the
// whole seconds left until the next one may.
// ponytail: in memory and per process; entries are dropped as they age out. Use a DB column if several instances ever run.
func (s *Server) payCheckWait(id int64) int {
	s.payCheckMu.Lock()
	defer s.payCheckMu.Unlock()
	now := time.Now()
	for k, t := range s.payCheckAt {
		if now.Sub(t) >= payCheckGap {
			delete(s.payCheckAt, k)
		}
	}
	if t, busy := s.payCheckAt[id]; busy {
		left := payCheckGap - now.Sub(t)
		return max(1, int((left+time.Second-1)/time.Second))
	}
	if s.payCheckAt == nil {
		s.payCheckAt = map[int64]time.Time{}
	}
	s.payCheckAt[id] = now
	return 0
}
