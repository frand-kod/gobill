package web

// Payment gateway selection, settlement and Tripay callback.

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/db"
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
