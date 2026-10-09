package web

// Admin session idle timeout and settings reload.

import (
	"log/slog"
	"net/http"

	"context"
	"strconv"
	"time"
)

const defaultIdle = 2 * time.Hour

// ReloadSessionSettings applies session_timeout_duration (minutes, admin idle timeout) and
// single_session. Called from New and from SettingsChanged (see main.go).
func (s *Server) ReloadSessionSettings(ctx context.Context) {
	m, err := s.loadSettings(ctx)
	if err != nil {
		slog.Error("session settings", "err", err)
		return
	}
	idle := defaultIdle
	if mins, err := strconv.Atoi(m["session_timeout_duration"]); err == nil && mins > 0 {
		idle = time.Duration(mins) * time.Minute
	}
	s.idle.Store(int64(idle))
	s.single.Store(m["single_session"] == "yes")
}

// idleGuard ends admin and portal sessions that were inactive for longer than the idle timeout.
// scs's own IdleTimeout is fixed at startup, so the runtime-changeable limit lives here.
func (s *Server) idleGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if s.sessions.GetInt64(ctx, "admin_id") != 0 || s.sessions.GetInt64(ctx, "customer_id") != 0 {
			now, limit := time.Now().Unix(), int64(defaultIdle/time.Second)
			if s.sessions.GetInt64(ctx, "admin_id") != 0 {
				limit = s.idle.Load() / int64(time.Second)
			}
			if seen := s.sessions.GetInt64(ctx, "seen"); seen > 0 && now-seen > limit {
				s.sessions.Destroy(ctx)
			} else if now-seen >= 30 { // avoid a session write on every request
				s.sessions.Put(ctx, "seen", now)
			}
		}
		next.ServeHTTP(w, r)
	})
}
