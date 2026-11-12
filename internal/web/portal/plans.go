package portal

// Customer plan purchase, extend, balance top-up and transfer.

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

type planRow struct {
	db.Plan
	Bandwidth string // shown when show_bandwidth_plan == yes
	Kind      string // friendly service type
	Duration  string // "30 Hari": validity with the translated unit
	Short     int64  // price minus balance when the balance is too low, else 0
}

type plansData struct {
	Plans    []planRow
	Balance  bool
	Channels []payment.Channel // online gateway channels; empty = gateway off
	Custom   bool              // allow_balance_custom with a gateway: custom top-up form
	HowToPay bool              // no gateway and the balance cannot buy any plan: say how to pay
	Voucher  bool              // vouchers are on: link them in the how-to-pay note
}

func (s *Handlers) pPlans(w http.ResponseWriter, r *http.Request) {
	s.plansPage(w, r, 200, "")
}

func (s *Handlers) plansPage(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	c := customerFrom(r)
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "portal plans", err)
		return
	}
	types := []string{"Hotspot", "PPPoE"}
	if c.ServiceType != "Others" {
		types = []string{c.ServiceType}
	}
	var plans []planRow
	for _, t := range types {
		l, err := s.Queries.ListEnabledPlansByType(r.Context(), t)
		if err != nil {
			s.Fail(w, "portal plans", err)
			return
		}
		isoPlan, _ := s.IsolirIDs(r.Context())
		for _, p := range l {
			if p.Billing == "prepaid" && p.ID != isoPlan {
				row := planRow{Plan: p, Kind: p.Type, Duration: fmt.Sprintf("%d %s", p.Validity, s.T(p.ValidityUnit)), Short: max(p.Price-c.Balance, 0)}
				if p.Type == "PPPoE" {
					row.Kind = s.T("Home internet")
				}
				if bw, err := s.Queries.GetBandwidth(r.Context(), p.BandwidthID.Int64); st["show_bandwidth_plan"] == "yes" && p.BandwidthID.Valid && err == nil {
					row.Bandwidth = bw.Name
				}
				plans = append(plans, row)
			}
		}
	}
	pd := plansData{Plans: plans, Balance: st["enable_balance"] != "no"}
	pd.Custom = pd.Balance && st["allow_balance_custom"] == "yes"
	if g, err := s.Gateway(st); g != nil && err == nil {
		if pd.Channels, err = s.ActiveChannels(r.Context(), g); err != nil {
			slog.Error("portal plans: channels", "err", err)
		}
	}
	pd.Voucher = st["disable_voucher"] != "yes"
	if len(pd.Channels) == 0 {
		pd.HowToPay = true
		for _, p := range plans {
			if pd.Balance && c.Balance >= p.Price {
				pd.HowToPay = false
			}
		}
	}
	s.Prender(w, r, code, "p_plans", core.Page{Title: "Order Package", Error: errMsg, Data: pd})
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
func (s *Handlers) pBuyBalance(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	st, err := s.LoadSettings(r.Context())
	if err != nil || s.Billing == nil {
		s.Fail(w, "portal buy", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["enable_balance"] == "no" {
		s.plansPage(w, r, 200, "Balance not enabled")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := s.Queries.GetPlan(r.Context(), id)
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
		s.plansPage(w, r, 200, s.T(m))
		return
	}
	if errors.Is(err, billing.ErrInsufficientBalance) {
		s.plansPage(w, r, 200, "Insufficient balance")
		return
	}
	if errors.Is(err, billing.ErrAdminOnlyPlan) {
		s.plansPage(w, r, 200, billing.ErrAdminOnlyPlan.Error())
		return
	}
	if errors.Is(err, billing.ErrInactive) {
		s.plansPage(w, r, 200, "account is not active")
		return
	}
	if err != nil {
		s.Fail(w, "portal buy", err)
		return
	}
	s.Sessions.Put(r.Context(), "flash", fmt.Sprintf("%s: %s", s.T("Package activated"), p.Name))
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// ---- balance transfer ----

func (s *Handlers) pTransfer(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.Fail(w, "portal transfer", errors.New("billing not configured"))
		return
	}
	amount, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("balance")), 10, 64)
	username := strings.TrimSpace(r.PostFormValue("username"))
	// the recipient's name for the flash, read before the transfer (an unknown user just leaves the name empty)
	to, _ := s.Queries.GetCustomerByUsername(r.Context(), username)
	err := s.Billing.TransferBalance(r.Context(), customerFrom(r).ID, username, amount)
	for _, e := range []error{billing.ErrSelfTransfer, billing.ErrTargetNotFound, billing.ErrBelowMinimum, billing.ErrTransferDisabled, billing.ErrInsufficientBalance, billing.ErrInactive} {
		if errors.Is(err, e) {
			msg := s.T(e.Error())
			if e == billing.ErrBelowMinimum {
				if st, _ := s.LoadSettings(r.Context()); st["minimum_transfer"] != "" {
					msg += " " + st["minimum_transfer"]
				}
			}
			s.profilePage(w, r, 200, msg)
			return
		}
	}
	if err != nil {
		s.Fail(w, "portal transfer", err)
		return
	}
	s.Sessions.Put(r.Context(), "flash", fmt.Sprintf(s.T("Sent %s to %s (%s)"), view.Money(amount), maskName(to.Fullname), username))
	http.Redirect(w, r, "/portal/profile", http.StatusSeeOther)
}

// pTransferLookup names the recipient for the confirm dialog. 404 when the username is unknown or transfers are off.
func (s *Handlers) pTransferLookup(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "portal transfer lookup", err)
		return
	}
	if st["enable_balance"] == "no" || st["allow_balance_transfer"] != "yes" {
		http.NotFound(w, r)
		return
	}
	if !s.lookupAllow(customerFrom(r).ID) {
		http.Error(w, "too many lookups", http.StatusTooManyRequests)
		return
	}
	c, err := s.Queries.GetCustomerByUsername(r.Context(), strings.TrimSpace(r.URL.Query().Get("username")))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.Fail(w, "portal transfer lookup", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"name": maskName(c.Fullname)})
}

// ---- self extend ----

func (s *Handlers) pExtend(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.Fail(w, "portal extend", errors.New("billing not configured"))
		return
	}
	until, err := s.Billing.ExtendExpired(r.Context(), customerFrom(r).ID, httpx.PathID(r))
	for _, e := range []error{billing.ErrExtendDisabled, billing.ErrExtendAlready, billing.ErrNotExpired, billing.ErrPlanNotFound, billing.ErrInactive, billing.ErrAdminOnlyPlan} {
		if errors.Is(err, e) {
			s.pDash(w, r, 200, s.T(e.Error()))
			return
		}
	}
	if err != nil {
		s.Fail(w, "portal extend", err)
		return
	}
	s.FlashTo(w, r, "/portal", s.T("Extend until")+" "+until.Format("2006-01-02"))
}

// maskName keeps the first word and the initials of the rest ("Budi Santoso" -> "Budi S."):
// enough for the sender to recognise the recipient, without handing any customer full names.
func maskName(full string) string {
	w := strings.Fields(full)
	for i := 1; i < len(w); i++ {
		w[i] = string([]rune(w[i])[:1]) + "."
	}
	return strings.Join(w, " ")
}

// lookupAllow caps recipient lookups at 30 per customer per 15 minutes (shares the OTP map and lock).
func (s *Handlers) lookupAllow(id int64) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.OtpSent == nil {
		s.OtpSent = map[string][]time.Time{}
	}
	k, now := fmt.Sprint("lk:", id), time.Now()
	var l []time.Time
	for _, t := range s.OtpSent[k] {
		if now.Sub(t) < 15*time.Minute {
			l = append(l, t)
		}
	}
	if len(l) >= 30 {
		return false
	}
	s.OtpSent[k] = append(l, now)
	return true
}
