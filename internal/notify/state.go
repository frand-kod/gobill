package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
)

// ChannelState is the send health of one channel since start, for the status page and alerts.
type ChannelState struct {
	Name         string
	Streak       int // consecutive failures; 0 after any success
	LastErr      string
	LastAt       time.Time
	Sent, Failed float64
}

var chans = struct {
	sync.Mutex
	m map[string]*ChannelState
}{m: map[string]*ChannelState{}}

// record counts one send attempt on channel ch in the metrics and keeps its streak and last error.
func record(ch string, err error) {
	chans.Lock()
	defer chans.Unlock()
	s := chans.m[ch]
	if s == nil {
		s = &ChannelState{Name: ch}
		chans.m[ch] = s
	}
	s.LastAt = time.Now()
	if err == nil {
		s.Streak = 0
		metrics.Inc("notifications_sent_total", "channel", ch)
		return
	}
	s.Streak++
	s.LastErr = scrub(err.Error())
	metrics.Inc("notifications_failed_total", "channel", ch)
}

// Channels returns the state of every channel that has been used since start.
func Channels() []ChannelState {
	chans.Lock()
	defer chans.Unlock()
	out := make([]ChannelState, 0, len(chans.m))
	for _, s := range chans.m {
		c := *s
		c.Sent = metrics.Value("notifications_sent_total", "channel", s.Name)
		c.Failed = metrics.Value("notifications_failed_total", "channel", s.Name)
		out = append(out, c)
	}
	return out
}

var (
	scrubURL   = regexp.MustCompile(`https?://\S+`)
	scrubToken = regexp.MustCompile(`\d{6,}:[A-Za-z0-9_-]+`)
	scrubPhone = regexp.MustCompile(`\+?\d{7,}`)
)

// Scrub is scrub for the web layer: error text without URLs, bot tokens or phone numbers.
func Scrub(s string) string { return scrub(s) }

// scrub removes URLs, bot tokens and phone-like numbers from error text before it is shown.
func scrub(s string) string {
	s = scrubURL.ReplaceAllString(s, "<url>")
	s = scrubToken.ReplaceAllString(s, "<token>")
	s = scrubPhone.ReplaceAllString(s, "<nomor>")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// clean drops the request URL from transport errors: gateway URLs carry API keys, the bot
// token and the message text.
func clean(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// LogTo returns a Log hook that stores each attempt in message_logs (body cut to 500 runes).
func LogTo(q *db.Queries) func(ch, to, subject, body string, err error) {
	return func(ch, to, subject, body string, err error) {
		status, msg := "ok", ""
		if err != nil {
			status, msg = "error", err.Error()
		}
		if r := []rune(body); len(r) > 500 {
			body = string(r[:500])
		}
		if e := q.CreateMessageLog(context.Background(), db.CreateMessageLogParams{Channel: ch, Recipient: to,
			Subject: subject, Body: body, Status: status, Error: msg}); e != nil {
			slog.Error("message log", "err", e)
		}
	}
}
