package notify

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Webhook POSTs {"id","event","time","data"} signed with X-Signature: sha256=HMAC(webhook_secret, body).
// The id (also in X-Event-Id) is random per delivery, so a receiver can drop repeats.
// Events: customer.activated, recharge.expired, payment.paid.
func (n *Notifier) Webhook(ctx context.Context, event string, payload any) error {
	_, err := n.PostWebhook(ctx, event, payload)
	return err
}

var webhookNoSecretWarned sync.Once

// PostWebhook is Webhook that also returns the HTTP status (0 when there is no URL or no answer).
// Every attempt is logged (channel webhook, recipient = the receiver's host so the URL's path and
// query stay out of the log).
func (n *Notifier) PostWebhook(ctx context.Context, event string, payload any) (int, error) {
	u := n.get("webhook_url")
	if u == "" {
		return 0, nil
	}
	secret := n.get("webhook_secret")
	if secret == "" {
		webhookNoSecretWarned.Do(func() {
			slog.Warn("webhook_url is set but webhook_secret is empty: webhooks are sent with an empty-key signature")
		})
	}
	var idb [16]byte
	rand.Read(idb[:])
	id := hex.EncodeToString(idb[:])
	body, err := json.Marshal(map[string]any{"id": id, "event": event, "time": time.Now().Unix(), "data": payload})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event", event)
	req.Header.Set("X-Event-Id", id)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	req.Header.Set("X-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	status, err := n.do(ctx, req)
	n.logged("webhook", req.URL.Host, event, string(body), err)
	return status, err
}
