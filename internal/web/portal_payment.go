package web

// Customer payment checkout and status check.

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/http"

	"errors"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
	"strconv"
	"strings"
	"time"
)

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
	if s.Billing != nil { // same formula as recharge: tax on the (discounted) price, then the customer's bills
		amount = s.Billing.OrderPrice(r.Context(), c.ID, amount)
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
		Gateway: g.Name(), CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Username: c.Username, PlanID: planID, Amount: amount, Coupon: code, Channel: channel, ExpiresAt: exp.Unix()})
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
