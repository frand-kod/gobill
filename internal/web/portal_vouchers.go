package web

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

func (s *Server) pVoucherForm(w http.ResponseWriter, r *http.Request) {
	s.prender(w, r, 200, "p_voucher", Page{Title: "Voucher Activation"})
}

func (s *Server) pVoucher(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil || s.Billing == nil {
		s.Fail(w, "portal voucher", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["disable_voucher"] == "yes" {
		s.prender(w, r, http.StatusForbidden, "p_voucher", Page{Title: "Voucher Activation", Error: "Voucher activation is disabled"})
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	keys := []string{"vip:" + core.ClientIP(r), "vc:" + strconv.FormatInt(customerFrom(r).ID, 10)}
	for _, k := range keys {
		if s.TooManyFailures(k) {
			s.prender(w, r, http.StatusTooManyRequests, "p_voucher", Page{Title: "Voucher Activation", Error: "Too many failed attempts. Try again in 15 minutes."})
			return
		}
	}
	switch err := s.Billing.RedeemVoucher(r.Context(), code, customerFrom(r).ID); {
	case code == "" || errors.Is(err, billing.ErrVoucherInvalid):
		for _, k := range keys {
			s.RecordFailure(k)
		}
		s.prender(w, r, 200, "p_voucher", Page{Title: "Voucher Activation", Error: "Voucher Not Valid"})
	case errors.Is(err, billing.ErrInactive):
		s.prender(w, r, 200, "p_voucher", Page{Title: "Voucher Activation", Error: "account is not active"})
	case err != nil:
		s.Fail(w, "portal voucher", err)
	default:
		to := "/portal/orders"
		if u := httpx.SafeRedirect(st["voucher_redirect"]); u != "" {
			to = u
		}
		s.FlashTo(w, r, to, "Activation Vouchers Successfully")
	}
}
