package portal

// Customer payment checkout and status check.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

// pPay is the gateway branch of the order: pending row, gateway transaction, redirect to pay_url.
// Coupons are validated against the plan price and reserved when the order is created (payOrder).
func (s *Handlers) pPay(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "portal pay", err)
		return
	}
	g, err := s.Gateway(st)
	if err != nil || g == nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.Queries.GetPlan(r.Context(), httpx.PathID(r))
	if errors.Is(err, sql.ErrNoRows) || err == nil && (p.Enabled != 1 || p.Type == "Balance" || p.Billing != "prepaid") {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "portal pay", err)
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
	chans, err := s.ActiveChannels(r.Context(), g)
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
		cp, err := s.Queries.GetCouponByCode(r.Context(), code)
		if errors.Is(err, sql.ErrNoRows) {
			err = billing.ErrCouponNotFound
		} else if err == nil {
			amount, err = billing.Discount(cp, p.Price, time.Now().In(s.Location()).Format("2006-01-02"))
			code = cp.Code
		}
		if err != nil {
			if m := couponErr(err); m != "" {
				s.plansPage(w, r, 200, s.T(m))
				return
			}
			s.Fail(w, "portal pay coupon", err)
			return
		}
	}
	if s.Billing != nil { // same formula as recharge: tax on the (discounted) price, then the customer's bills
		if amount, err = s.Billing.OrderPrice(r.Context(), c.ID, amount); err != nil {
			s.Fail(w, "portal pay price", err)
			return
		}
	}
	s.payOrder(w, r, g, c, p.ID, p.Name, amount, code, channel)
}

// pTopUp is allow_balance_custom (only with a gateway): a custom amount paid online is added to the balance.
func (s *Handlers) pTopUp(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "portal topup", err)
		return
	}
	g, err := s.Gateway(st)
	if err != nil || g == nil || st["allow_balance_custom"] != "yes" || st["enable_balance"] == "no" {
		http.NotFound(w, r)
		return
	}
	amount, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("amount")), 10, 64)
	chans, err := s.ActiveChannels(r.Context(), g)
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
func (s *Handlers) payOrder(w http.ResponseWriter, r *http.Request, g core.Gateway, c *db.Customer, planID int64, planName string, amount int64, code, channel string) {
	if code != "" { // reserve the coupon use now; refused when it ran out since the price was shown
		if err := billing.ReserveCoupon(r.Context(), s.Queries, code); err != nil {
			if m := couponErr(err); m != "" {
				s.plansPage(w, r, 200, s.T(m))
				return
			}
			s.Fail(w, "portal pay coupon", err)
			return
		}
	}
	raw := make([]byte, 8)
	rand.Read(raw)
	exp := time.Now().Add(core.PayExpiry)
	pr, err := s.Queries.CreatePaymentRequest(r.Context(), db.CreatePaymentRequestParams{Ref: "NB" + strings.ToUpper(hex.EncodeToString(raw)),
		Gateway: g.Name(), CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Username: c.Username, PlanID: planID, Amount: amount, Coupon: code, Channel: channel, ExpiresAt: exp.Unix()})
	if err != nil {
		if code != "" {
			s.Queries.ReleaseCoupon(r.Context(), code)
		}
		s.Fail(w, "portal pay", err)
		return
	}
	base := core.BaseURL(r)
	if st, err := s.LoadSettings(r.Context()); err == nil && st["app_url"] != "" {
		base = strings.TrimRight(st["app_url"], "/")
	}
	cr, err := g.CreateTransaction(r.Context(), payment.Transaction{ID: pr.Ref, Amount: amount, PlanName: planName, Name: c.Fullname,
		Email: c.Email, Phone: c.Phone, ReturnURL: core.BaseURL(r) + "/portal/payments/" + strconv.FormatInt(pr.ID, 10), CallbackURL: base + "/callback/" + g.Name(), Channel: channel, Expiry: exp})
	if err != nil {
		slog.Error("pay: create transaction", "ref", pr.Ref, "err", err)
		s.Queries.ClosePaymentRequest(r.Context(), db.ClosePaymentRequestParams{Status: "failed", Ref: pr.Ref})
		s.plansPage(w, r, http.StatusBadGateway, "Payment gateway error, try again later")
		return
	}
	if err := s.Queries.SetPaymentGatewayRef(r.Context(), db.SetPaymentGatewayRefParams{GatewayRef: cr.Reference, PayUrl: cr.PayURL, ID: pr.ID}); err != nil {
		s.Fail(w, "portal pay", err)
		return
	}
	http.Redirect(w, r, cr.PayURL, http.StatusSeeOther)
}

// ownPayment loads the payment of the logged-in customer (404 for anyone else's).
func (s *Handlers) ownPayment(w http.ResponseWriter, r *http.Request) (db.PaymentRequest, bool) {
	pr, err := s.Queries.GetPaymentRequest(r.Context(), httpx.PathID(r))
	if err != nil || !pr.CustomerID.Valid || pr.CustomerID.Int64 != customerFrom(r).ID { // NULL = customer deleted: not yours
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Error("load payment", "err", err)
		}
		http.NotFound(w, r)
		return pr, false
	}
	return pr, true
}

func (s *Handlers) pPayView(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.ownPayment(w, r)
	if !ok {
		return
	}
	pl, _ := s.Queries.GetPlan(r.Context(), pr.PlanID)
	if pr.PlanID == 0 {
		pl.Name = "Custom Balance"
	}
	s.Prender(w, r, 200, "p_payment", core.Page{Title: "Order Details", Data: struct {
		P       db.PaymentRequest
		Plan    string
		Channel string
	}{pr, pl.Name, s.ChannelName(pr.Channel)}})
}

// pendingPay is the customer's newest unpaid payment that has not expired, nil when there is none.
func (s *Handlers) pendingPay(ctx context.Context, customerID int64) *db.PaymentRequest {
	pr, err := s.Queries.GetPendingPaymentRequestByCustomer(ctx, db.GetPendingPaymentRequestByCustomerParams{CustomerID: sql.NullInt64{Int64: customerID, Valid: true}, ExpiresAt: time.Now().Unix()})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("pending payment", "err", err)
		}
		return nil
	}
	return &pr
}

// pPayCheck asks the gateway for the status, for when the callback did not arrive.
func (s *Handlers) pPayCheck(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.ownPayment(w, r)
	if !ok {
		return
	}
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "portal pay check", err)
		return
	}
	g, err := s.Gateway(st)
	if err != nil || g == nil {
		http.NotFound(w, r)
		return
	}
	back := "/portal/payments/" + strconv.FormatInt(pr.ID, 10)
	if pr.Status == "pending" && pr.GatewayRef != "" {
		if wait := s.PayCheckWait(pr.ID); wait > 0 {
			s.Sessions.Put(r.Context(), "flash", fmt.Sprintf(s.T("Wait %d seconds before checking again"), wait))
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		got, err := g.Status(r.Context(), payment.Transaction{ID: pr.Ref, Reference: pr.GatewayRef})
		if err != nil {
			slog.Error("pay: status", "ref", pr.Ref, "err", err)
			s.FlashTo(w, r, back, "Payment gateway error, try again later")
			return
		}
		if err := s.SettlePayment(r.Context(), pr, got); err != nil {
			s.Fail(w, "portal pay check", err)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// PayCheckGap is the minimum time between gateway status calls for one payment (Duitku blocks the
// merchant for about an hour on excess status calls).
const PayCheckGap = 30 * time.Second

// PayCheckWait records a status call for payment id. It returns 0 when the call may go ahead, otherwise the
// whole seconds left until the next one may.
// ponytail: in memory and per process; entries are dropped as they age out. Use a DB column if several instances ever run.
func (s *Handlers) PayCheckWait(id int64) int {
	s.PayCheckMu.Lock()
	defer s.PayCheckMu.Unlock()
	now := time.Now()
	for k, t := range s.PayCheckAt {
		if now.Sub(t) >= PayCheckGap {
			delete(s.PayCheckAt, k)
		}
	}
	if t, busy := s.PayCheckAt[id]; busy {
		left := PayCheckGap - now.Sub(t)
		return max(1, int((left+time.Second-1)/time.Second))
	}
	if s.PayCheckAt == nil {
		s.PayCheckAt = map[int64]time.Time{}
	}
	s.PayCheckAt[id] = now
	return 0
}
