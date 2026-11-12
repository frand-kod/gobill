package portal

// Customer voucher redemption.

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"errors"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/billing"
)

// ---- voucher activation ----

func (s *Handlers) pVoucherForm(w http.ResponseWriter, r *http.Request) {
	s.Prender(w, r, 200, "p_voucher", core.Page{Title: "Voucher Activation"})
}

func (s *Handlers) pVoucher(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil || s.Billing == nil {
		s.Fail(w, "portal voucher", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["disable_voucher"] == "yes" {
		s.Prender(w, r, http.StatusForbidden, "p_voucher", core.Page{Title: "Voucher Activation", Error: "Voucher activation is disabled"})
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	keys := []string{"vip:" + core.ClientIP(r), "vc:" + strconv.FormatInt(customerFrom(r).ID, 10)}
	if !s.ReserveAll(core.MaxFailedLogins, keys...) {
		s.Prender(w, r, http.StatusTooManyRequests, "p_voucher", core.Page{Title: "Voucher Activation", Error: "Too many failed attempts. Try again in 15 minutes."})
		return
	}
	// the attempts are already counted; only a wrong code keeps them
	switch err := s.Billing.RedeemVoucher(r.Context(), code, customerFrom(r).ID); {
	case code == "" || errors.Is(err, billing.ErrVoucherInvalid):
		s.Prender(w, r, 200, "p_voucher", core.Page{Title: "Voucher Activation", Error: "Voucher Not Valid"})
	case errors.Is(err, billing.ErrInactive):
		s.Unreserve(keys[0])
		s.Unreserve(keys[1])
		s.Prender(w, r, 200, "p_voucher", core.Page{Title: "Voucher Activation", Error: "account is not active"})
	case err != nil:
		s.Unreserve(keys[0])
		s.Unreserve(keys[1])
		s.Fail(w, "portal voucher", err)
	default:
		s.Unreserve(keys[0])
		s.Unreserve(keys[1])
		to := "/portal/orders"
		if u := httpx.SafeRedirect(st["voucher_redirect"]); u != "" {
			to = u
		}
		s.FlashTo(w, r, to, "Activation Vouchers Successfully")
	}
}
