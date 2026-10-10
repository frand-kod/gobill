package billing

// Router connections, ping, pool sync and health check.

import (
	"database/sql"
	"log/slog"

	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
	"net"
	"strconv"
	"time"
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

// routerConn builds the connection info of a stored router; RouterFor overrides it in tests.
func (s *Service) routerConn(r db.Router) (device.Router, error) {
	if s.RouterFor != nil {
		return s.RouterFor(r)
	}
	pw, err := secret.Open(s.Key, r.PasswordEnc)
	if err != nil {
		return device.Router{}, fmt.Errorf("decrypt router password: %w", err)
	}
	return device.Router{Addr: net.JoinHostPort(r.Host, strconv.FormatInt(r.Port, 10)), User: r.Username, Pass: string(pw), TLS: r.Port == 8729}, nil
}

// Ping connects to the router and returns its identity.
func (s *Service) Ping(ctx context.Context, r db.Router) (string, error) {
	rt, err := s.routerConn(r)
	if err != nil {
		return "", err
	}
	return rt.Ping(ctx)
}

// Health reads the live state of a stored router (resources and active sessions).
func (s *Service) Health(ctx context.Context, r db.Router) (device.Health, error) {
	rt, err := s.routerConn(r)
	if err != nil {
		return device.Health{}, err
	}
	return rt.Health(ctx)
}

// SyncPool pushes an admin pool change to the pool's router (op: add, update, remove).
func (s *Service) SyncPool(ctx context.Context, op string, p db.Pool, oldName string) error {
	r, err := s.Q.GetRouter(ctx, p.RouterID)
	if err != nil {
		return err
	}
	rt, err := s.routerConn(r)
	if err != nil {
		return err
	}
	return rt.SyncPool(ctx, op, oldName, device.Pool{Name: p.Name, Ranges: p.RangeIp})
}
