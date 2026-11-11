// Package notify ports PHPNuxBill's Message.php: Telegram, URL-gateway SMS/WhatsApp, SMTP email
// and an outgoing signed webhook (replacing the old plugin hooks). Setting keys keep the old
// appconfig names. Everything is a no-op when its config is empty. Use Go to send without
// blocking billing.
package notify

import (
	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/settings"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

type Notifier struct {
	Settings    map[string]string
	HTTP        *http.Client
	TelegramAPI string // default https://api.telegram.org
	Timeout     time.Duration
	// Log, if set, is called after every send attempt (channel: telegram, sms, wa, email);
	// it records the attempt without notify knowing about the database.
	Log func(channel, recipient, subject, body string, err error)
}

func (n *Notifier) logged(ch, to, subject, body string, err error) error {
	err = clean(err)
	if n.Log != nil {
		n.Log(ch, to, subject, body, err)
	}
	record(ch, err)
	return err
}

// Load reads all settings.
func Load(ctx context.Context, q *db.Queries) (*Notifier, error) {
	st, err := settings.List(ctx, q)
	if err != nil {
		return nil, err
	}
	n := &Notifier{Settings: st, HTTP: &http.Client{Timeout: 20 * time.Second}, TelegramAPI: "https://api.telegram.org", Timeout: 20 * time.Second}
	n.Log = LogTo(q)
	return n, nil
}

// MessageDelay is the pause between bulk messages: setting message_delay in seconds, default 1.
func MessageDelay(st map[string]string) time.Duration {
	sec, err := strconv.ParseFloat(st["message_delay"], 64)
	if err != nil || sec < 0 {
		sec = 1
	}
	return time.Duration(sec * float64(time.Second))
}

func (n *Notifier) get(k string) string { return n.Settings[k] }

// Go runs fn in a goroutine with a timeout; errors are only logged.
func (n *Notifier) Go(name string, fn func(context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), n.Timeout)
		defer cancel()
		if err := fn(ctx); err != nil {
			slog.Error("notify failed", "kind", name, "err", err)
		}
	}()
}

// do sends req and returns the HTTP status (0 when there was no answer).
func (n *Notifier) do(ctx context.Context, req *http.Request) (int, error) {
	resp, err := n.HTTP.Do(req.WithContext(ctx))
	if err != nil {
		return 0, clean(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s: HTTP %d", req.URL.Host, resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func (n *Notifier) getURL(ctx context.Context, u string) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return clean(err)
	}
	_, err = n.do(ctx, req)
	return err
}

// Config returns the settings the notifier was loaded with (read only).
func (n *Notifier) Config() map[string]string { return n.Settings }

// SendTimeout is the time limit for one send.
func (n *Notifier) SendTimeout() time.Duration { return n.Timeout }
