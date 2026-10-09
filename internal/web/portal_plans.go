package web

// Customer plan purchase, extend, balance top-up and transfer.

import (
	"database/sql"
	"log/slog"
	"net/http"

	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
	"strconv"
	"strings"
)

type planRow struct {
	db.Plan
	Bandwidth string // shown when show_bandwidth_plan == yes
}

type plansData struct {
	Plans    []planRow
	Balance  bool
	Channels []payment.Channel // online gateway channels; empty = gateway off
	Custom   bool              // allow_balance_custom with a gateway: custom top-up form
}

func (s *Server) pPlans(w http.ResponseWriter, r *http.Request) {
	s.plansPage(w, r, 200, "")
}

func (s *Server) plansPage(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	c := customerFrom(r)
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal plans", err)
		return
	}
	types := []string{"Hotspot", "PPPoE"}
	if c.ServiceType != "Others" {
		types = []string{c.ServiceType}
	}
	var plans []planRow
	for _, t := range types {
		l, err := s.queries.ListEnabledPlansByType(r.Context(), t)
		if err != nil {
			s.fail(w, "portal plans", err)
			return
		}
		for _, p := range l {
			if p.Billing == "prepaid" {
				row := planRow{Plan: p}
				if bw, err := s.queries.GetBandwidth(r.Context(), p.BandwidthID.Int64); st["show_bandwidth_plan"] == "yes" && p.BandwidthID.Valid && err == nil {
					row.Bandwidth = bw.Name
				}
				plans = append(plans, row)
			}
		}
	}
	pd := plansData{Plans: plans, Balance: st["enable_balance"] != "no"}
	pd.Custom = pd.Balance && st["allow_balance_custom"] == "yes"
	if g, err := s.gateway(st); g != nil && err == nil {
		if pd.Channels, err = s.activeChannels(r.Context(), g); err != nil {
			slog.Error("portal plans: channels", "err", err)
		}
	}
	s.prender(w, r, code, "p_plans", Page{Title: "Order Package", Error: errMsg, Data: pd})
}

// couponErr returns the user-facing message of a coupon validation error, or "".
func couponErr(err error) string {
	for _, e := range []error{billing.ErrCouponNotFound, billing.ErrCouponInactive, billing.ErrCouponDate, billing.ErrCouponUsed, billing.ErrCouponMin, billing.ErrCouponTooBig} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return ""
}

// pBuyBalance pays a plan from the customer's balance.
// The online gateway (Tripay) order is pPay in payment.go.
func (s *Server) pBuyBalance(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	st, err := s.loadSettings(r.Context())
	if err != nil || s.Billing == nil {
		s.fail(w, "portal buy", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["enable_balance"] == "no" {
		s.plansPage(w, r, 200, "Balance not enabled")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := s.queries.GetPlan(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (p.Enabled != 1 || p.Type == "Balance" || p.Billing != "prepaid") {
		http.NotFound(w, r)
		return
	}
	if err == nil {
		if code := strings.TrimSpace(r.PostFormValue("coupon")); code != "" {
			err = s.Billing.RechargeWithBalanceCoupon(r.Context(), c.ID, p.ID, code)
		} else {
			err = s.Billing.RechargeWithBalance(r.Context(), c.ID, p.ID, 0)
		}
	}
	if m := couponErr(err); m != "" {
		s.plansPage(w, r, 200, s.catalog.T(s.language(), m))
		return
	}
	if errors.Is(err, billing.ErrInsufficientBalance) {
		s.plansPage(w, r, 200, "Insufficient balance")
		return
	}
	if errors.Is(err, billing.ErrInactive) {
		s.plansPage(w, r, 200, "account is not active")
		return
	}
	if err != nil {
		s.fail(w, "portal buy", err)
		return
	}
	s.sessions.Put(r.Context(), "flash", fmt.Sprintf("%s: %s", s.catalog.T(s.language(), "Package activated"), p.Name))
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// ---- balance transfer ----

func (s *Server) pTransfer(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.fail(w, "portal transfer", errors.New("billing not configured"))
		return
	}
	amount, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("balance")), 10, 64)
	err := s.Billing.TransferBalance(r.Context(), customerFrom(r).ID, strings.TrimSpace(r.PostFormValue("username")), amount)
	for _, e := range []error{billing.ErrSelfTransfer, billing.ErrTargetNotFound, billing.ErrBelowMinimum, billing.ErrTransferDisabled, billing.ErrInsufficientBalance, billing.ErrInactive} {
		if errors.Is(err, e) {
			msg := s.catalog.T(s.language(), e.Error())
			if e == billing.ErrBelowMinimum {
				if st, _ := s.loadSettings(r.Context()); st["minimum_transfer"] != "" {
					msg += " " + st["minimum_transfer"]
				}
			}
			s.pDash(w, r, 200, msg)
			return
		}
	}
	if err != nil {
		s.fail(w, "portal transfer", err)
		return
	}
	s.flashTo(w, r, "/portal", s.catalog.T(s.language(), "Sending balance success")+": "+money(amount)+" -> "+strings.TrimSpace(r.PostFormValue("username")))
}

// ---- self extend ----

func (s *Server) pExtend(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.fail(w, "portal extend", errors.New("billing not configured"))
		return
	}
	until, err := s.Billing.ExtendExpired(r.Context(), customerFrom(r).ID, pathID(r))
	for _, e := range []error{billing.ErrExtendDisabled, billing.ErrExtendAlready, billing.ErrNotExpired, billing.ErrPlanNotFound, billing.ErrInactive} {
		if errors.Is(err, e) {
			s.pDash(w, r, 200, s.catalog.T(s.language(), e.Error()))
			return
		}
	}
	if err != nil {
		s.fail(w, "portal extend", err)
		return
	}
	s.flashTo(w, r, "/portal", s.catalog.T(s.language(), "Extend until")+" "+until.Format("2006-01-02"))
}
