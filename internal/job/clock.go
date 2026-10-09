package job

import (
	"context"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

const (
	keyLastSeen  = "clock_last_seen_at"
	keyGuard     = "clock_guard"
	reasonBack   = "System clock went backwards; expiry is paused until it catches up."
	reasonUnsync = "System clock is not synchronised with NTP; expiry is paused."
)

// ClockGuard decides whether the system clock can be trusted. STBs have no RTC,
// so after a reboot the clock may be far in the past until NTP syncs.
type ClockGuard struct {
	Q      *db.Queries
	Now    func() time.Time       // injectable for tests
	Synced func() bool            // injectable for tests; false when the kernel says NTP is unsynchronised
	state  atomic.Pointer[string] // nil = trusted, otherwise the reason
}

func NewClockGuard(q *db.Queries) *ClockGuard {
	return &ClockGuard{Q: q, Now: time.Now, Synced: ntpSynced}
}

// Trusted reports the result of the last Check. Before the first Check it is true.
func (g *ClockGuard) Trusted() bool { return g.state.Load() == nil }

// Reason is empty when the clock is trusted.
func (g *ClockGuard) Reason() string {
	if r := g.state.Load(); r != nil {
		return *r
	}
	return ""
}

// Check runs one tick: update the verdict and, if trusted, move last_seen_at forward.
func (g *ClockGuard) Check(ctx context.Context) error {
	settings, err := g.Q.ListSettings(ctx)
	if err != nil {
		return err
	}
	var guard, last string
	for _, s := range settings {
		switch s.Key {
		case keyGuard:
			guard = s.Value
		case keyLastSeen:
			last = s.Value
		}
	}
	if guard == "off" {
		g.state.Store(nil)
		return nil
	}
	now := g.Now().Unix()
	seen, _ := strconv.ParseInt(last, 10, 64) // missing or bad value = 0
	reason := ""
	switch {
	case now < seen:
		reason = reasonBack
	case !g.Synced():
		reason = reasonUnsync
	}
	if reason != "" {
		if old := g.state.Swap(&reason); old == nil || *old != reason {
			slog.Warn("clock untrusted", "reason", reason, "now", now, "last_seen_at", seen)
		}
		return nil
	}
	g.state.Store(nil)
	return g.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: keyLastSeen, Value: strconv.FormatInt(now, 10)})
}

// Run checks once immediately, then every minute until ctx is done.
func (g *ClockGuard) Run(ctx context.Context) {
	if err := g.Check(ctx); err != nil {
		slog.Error("clock guard", "err", err)
	}
	Run(ctx, "clock_guard", time.Minute, g.Check)
}
