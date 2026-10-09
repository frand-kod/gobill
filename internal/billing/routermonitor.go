package billing

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// RouterCheck pings every enabled router, stores online/last_seen_at, and sends one Telegram
// alert per state change (not per tick). router_check = "no" turns it off; unset counts as yes.
// It is the job.Run body of the old router_check cron.
func (s *Service) RouterCheck(ctx context.Context) error {
	if setting(ctx, s.Q, "router_check") == "no" {
		return nil
	}
	rs, err := s.Q.ListEnabledRouters(ctx)
	if err != nil {
		return err
	}
	for _, r := range rs {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, perr := s.Ping(pctx, r)
		cancel()
		up := perr == nil
		p := db.SetRouterStatusParams{ID: r.ID, Online: sql.NullInt64{Valid: true}}
		if up {
			p.Online.Int64 = 1
			p.LastSeenAt = sql.NullInt64{Int64: s.now().Unix(), Valid: true}
		}
		if err := s.Q.SetRouterStatus(ctx, p); err != nil {
			return err
		}
		// alert on a change; the very first check only alerts when the router is down
		known, wasUp := r.Online.Valid, r.Online.Int64 == 1
		if (known && wasUp == up) || (!known && up) {
			continue
		}
		msg := "Router " + r.Name + " is back online"
		if !up {
			msg = "Router " + r.Name + " is OFFLINE: " + perr.Error()
		}
		if nf := s.notifier(); nf != nil {
			if err := nf.Telegram(ctx, msg); err != nil {
				slog.Error("router alert", "router", r.Name, "err", err)
			}
		}
	}
	return nil
}
