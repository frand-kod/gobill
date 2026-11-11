package core

// Payment gateway selection and settlement.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
	"github.com/frand-kod/gobill/internal/web/view"
)

// Gateway is what the web layer needs from an online gateway (Tripay today).
type Gateway interface {
	payment.PaymentGateway
	Channels(ctx context.Context) ([]payment.Channel, error)
}

// paymentCache holds the active channel list for ChannelTTL.
// ponytail: single-gateway cache, not cleared when the keys change (expires in 10 min).
type paymentCache struct {
	mu   sync.Mutex
	at   time.Time
	list []payment.Channel
}

const (
	ChannelTTL = 10 * time.Minute
	PayExpiry  = 24 * time.Hour
)

// Gateway builds the configured gateway from the tripay_* settings; nil when none is selected.
func (s *Core) Gateway(st map[string]string) (Gateway, error) {
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

func GatewayLabel(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// ActiveChannels returns the usable channels, cached.
func (s *Core) ActiveChannels(ctx context.Context, g Gateway) ([]payment.Channel, error) {
	c := &s.ChanCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > ChannelTTL || c.list == nil {
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

func BaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// SettlePayment applies a gateway status. Paid claims the row and recharges in one transaction,
// so it happens at most once however often the callback and "check status" fire.
func (s *Core) SettlePayment(ctx context.Context, pr db.PaymentRequest, st payment.Status) error {
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
			n, err := s.Queries.ClaimPaymentPaid(ctx, pr.Ref)
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
			return s.Billing.TopUpPaid(ctx, claim, pr.CustomerID.Int64, pr.Amount, GatewayLabel(pr.Gateway)+" - "+pr.Channel)
		}
		return s.Billing.RechargePaid(ctx, claim, pr.CustomerID.Int64, pr.PlanID, GatewayLabel(pr.Gateway)+" - "+pr.Channel, pr.Coupon, pr.Amount)
	case payment.Refunded:
		if pr.Status != "paid" { // never settled: nothing to take back, just close it
			_, err := s.Queries.ClosePaymentRequest(ctx, db.ClosePaymentRequestParams{Status: "failed", Ref: pr.Ref})
			return err
		}
		// ponytail: payment_requests.status has no 'refunded' value, so the record is the activity log;
		// a repeated callback logs and alerts again. Add a status via table rebuild if that gets noisy.
		slog.Warn("payment refunded by the gateway after it was settled", "ref", pr.Ref)
		msg := fmt.Sprintf("Gateway melaporkan REFUND/chargeback untuk pembayaran yang sudah lunas: tidak dibatalkan otomatis, cek manual\nGateway: %s %s\nRef: %s\nJumlah: %s\nUsername: %s",
			pr.Gateway, pr.Channel, pr.Ref, view.Money(pr.Amount), pr.Username)
		if err := s.Queries.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "system", Action: "payment_refunded", Description: msg}); err != nil {
			slog.Error("log refund", "ref", pr.Ref, "err", err)
		}
		if s.Billing != nil {
			if err := s.Billing.Alert(ctx, msg); err != nil {
				slog.Error("operator alert", "ref", pr.Ref, "err", err)
			}
		}
		return nil
	case payment.Failed, payment.Expired:
		_, err := s.Queries.ClosePaymentRequest(ctx, db.ClosePaymentRequestParams{Status: string(st), Ref: pr.Ref})
		return err
	}
	return nil
}

// ChannelName is the gateway's name for a channel code, from the cached list; the code itself when unknown.
func (s *Core) ChannelName(code string) string {
	s.ChanCache.mu.Lock()
	defer s.ChanCache.mu.Unlock()
	for _, ch := range s.ChanCache.list {
		if ch.Code == code && ch.Name != "" {
			return ch.Name
		}
	}
	return code
}
