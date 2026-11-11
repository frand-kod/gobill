package web

// Payment gateway selection, settlement and Tripay callback.

import (
	"encoding/json"
	"fmt"
	"github.com/frand-kod/gobill/internal/web/view"
	"log/slog"
	"net/http"
	"strings"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/payment"
	"sync"
	"time"
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
	var cfg map[string]string
	switch st["payment_gateway"] {
	case "duitku":
		cfg = map[string]string{"api_key": st["duitku_api_key"], "merchant_code": st["duitku_merchant_code"],
			"mode": st["duitku_mode"], "channel": st["duitku_channel"]}
		if s.NewGateway != nil {
			return s.NewGateway(cfg)
		}
		return payment.NewDuitku(cfg)
	case "tripay":
	default:
		return nil, nil
	}
	cfg = map[string]string{"api_key": st["tripay_api_key"], "private_key": st["tripay_private_key"],
		"merchant_code": st["tripay_merchant_code"], "mode": st["tripay_mode"], "channel": st["tripay_channel"]}
	if s.NewGateway != nil {
		return s.NewGateway(cfg)
	}
	return payment.NewTripay(cfg)
}

func gatewayLabel(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
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
		if !pr.CustomerID.Valid { // customer deleted after ordering: nothing to recharge; record the payment as paid and tell the operator
			slog.Warn("payment paid for a deleted customer", "ref", pr.Ref, "username", pr.Username)
			n, err := s.queries.ClaimPaymentPaid(ctx, pr.Ref)
			if err != nil || n == 0 { // n == 0: an earlier callback claimed it and already alerted
				return err
			}
			msg := fmt.Sprintf("Pembayaran diterima untuk pelanggan yang sudah dihapus: perlu refund/recharge manual\nGateway: %s %s\nRef: %s\nJumlah: %s\nUsername: %s",
				pr.Gateway, pr.Channel, pr.Ref, view.Money(pr.Amount), pr.Username)
			if err := s.Billing.Alert(ctx, msg); err != nil {
				slog.Error("operator alert", "ref", pr.Ref, "err", err)
			}
			return nil
		}
		claim := func(q *db.Queries) (bool, error) {
			n, err := q.ClaimPaymentPaid(ctx, pr.Ref)
			return n > 0, err
		}
		if pr.PlanID == 0 { // custom balance top-up
			return s.Billing.TopUpPaid(ctx, claim, pr.CustomerID.Int64, pr.Amount, gatewayLabel(pr.Gateway)+" - "+pr.Channel)
		}
		return s.Billing.RechargePaid(ctx, claim, pr.CustomerID.Int64, pr.PlanID, gatewayLabel(pr.Gateway)+" - "+pr.Channel, pr.Coupon, pr.Amount)
	case payment.Refunded:
		if pr.Status != "paid" { // never settled: nothing to take back, just close it
			_, err := s.queries.ClosePaymentRequest(ctx, db.ClosePaymentRequestParams{Status: "failed", Ref: pr.Ref})
			return err
		}
		// ponytail: payment_requests.status has no 'refunded' value, so the record is the activity log;
		// a repeated callback logs and alerts again. Add a status via table rebuild if that gets noisy.
		slog.Warn("payment refunded by the gateway after it was settled", "ref", pr.Ref)
		msg := fmt.Sprintf("Gateway melaporkan REFUND/chargeback untuk pembayaran yang sudah lunas: tidak dibatalkan otomatis, cek manual\nGateway: %s %s\nRef: %s\nJumlah: %s\nUsername: %s",
			pr.Gateway, pr.Channel, pr.Ref, view.Money(pr.Amount), pr.Username)
		if err := s.queries.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "system", Action: "payment_refunded", Description: msg}); err != nil {
			slog.Error("log refund", "ref", pr.Ref, "err", err)
		}
		if s.Billing != nil {
			if err := s.Billing.Alert(ctx, msg); err != nil {
				slog.Error("operator alert", "ref", pr.Ref, "err", err)
			}
		}
		return nil
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
	ref, status, amount, err := g.HandleCallback(r)
	metrics.Set("payment_callback_last_timestamp", float64(time.Now().Unix()))
	if err != nil {
		metrics.Inc("payment_callbacks_total", "result", "failed")
		slog.Warn("tripay callback rejected", "ip", clientIP(r), "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	pr, err := s.queries.GetPaymentRequestByRef(r.Context(), ref)
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
	} else if err := s.settlePayment(r.Context(), pr, status); err != nil {
		metrics.Inc("payment_callbacks_total", "result", "failed")
		s.fail(w, "tripay callback settle "+ref, err) // 500 makes Tripay retry
		return
	}
	metrics.Inc("payment_callbacks_total", "result", "ok")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
