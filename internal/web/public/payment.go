package public

// Payment gateway selection, settlement and Tripay callback.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/view"

	"time"

	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/payment"
)

// tripayCallback is the server-to-server notification. CSRF-exempt (see Handler); the HMAC
// signature is the authentication. A valid signature always gets {"success":true}, so the
// gateway stops retrying even for unknown or already-handled references.
func (s *Handlers) tripayCallback(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "tripay callback", err)
		return
	}
	g, err := s.Gateway(st)
	if err != nil || g == nil {
		http.NotFound(w, r)
		return
	}
	ref, status, amount, err := g.HandleCallback(r)
	metrics.Set("payment_callback_last_timestamp", float64(time.Now().Unix()))
	if err != nil {
		metrics.Inc("payment_callbacks_total", "result", "failed")
		slog.Warn("tripay callback rejected", "ip", core.ClientIP(r), "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	pr, err := s.Queries.GetPaymentRequestByRef(r.Context(), ref)
	if err != nil {
		if status == payment.Paid { // money arrived for a request we do not have: someone must settle it by hand
			metrics.Inc("payment_callbacks_total", "result", "unknown_paid")
			slog.Error("payment callback: PAID for an unknown reference, settle it by hand", "ref", ref, "amount", amount, "err", err)
			if s.Billing != nil {
				msg := fmt.Sprintf("Pembayaran diterima untuk referensi yang tidak dikenal: perlu cek dan recharge manual\nRef: %s\nJumlah: %s", ref, view.Money(amount))
				if err := s.Billing.Alert(r.Context(), msg); err != nil {
					slog.Error("operator alert", "ref", ref, "err", err)
				}
			}
		} else {
			slog.Warn("payment callback: unknown reference", "ref", ref, "err", err)
		}
	} else if (amount != 0 || status == payment.Paid) && amount != pr.Amount { // paid without an amount is refused too
		metrics.Inc("payment_callbacks_total", "result", "amount_mismatch")
		slog.Warn("tripay callback: amount mismatch", "ref", ref, "got", amount, "want", pr.Amount)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	} else if err := s.SettlePayment(r.Context(), pr, status); err != nil {
		metrics.Inc("payment_callbacks_total", "result", "failed")
		s.Fail(w, "tripay callback settle "+ref, err) // 500 makes Tripay retry
		return
	}
	metrics.Inc("payment_callbacks_total", "result", "ok")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
