package radius

import (
	"context"
	"log/slog"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// BoostJob moves the online sessions of plans with a scheduled boost to the right speed when the boost
// window opens or closes: a CoA-Request (CoA computes the rate for the moment), and a
// Disconnect-Request when the NAS refuses it (the customer reconnects and Access-Accept carries the
// right rate). A new login already gets the right rate from authorize.
// ponytail: runs once a minute, so the switch is up to a minute late; the window state each session was
// last given is in memory, a session met for the first time is compared with the state at its start.
func BoostJob(q *db.Queries, key []byte, port string) func(context.Context) error {
	given := map[int64]bool{}
	return func(ctx context.Context) error {
		ss, err := q.ListBoostSessions(ctx)
		if err != nil {
			return err
		}
		now := time.Now().In(appLoc())
		open := map[int64]bool{}
		for _, s := range ss {
			open[s.ID] = true
			lr, err := q.GetRadiusLogin(ctx, s.Username)
			if err != nil {
				continue
			}
			on, was := boostOn(lr.BoostWindow, now), false
			if prev, ok := given[s.ID]; ok {
				was = prev
			} else {
				was = boostOn(lr.BoostWindow, time.Unix(s.StartedAt, 0).In(appLoc()))
			}
			if on == was {
				given[s.ID] = on
				continue
			}
			err = CoA(ctx, q, key, port, s, "", now)
			if err != nil {
				slog.Warn("radius: boost CoA failed, disconnecting", "user", s.Username, "nas", s.NasIp, "err", err)
				err = Disconnect(ctx, q, key, port, s)
			}
			if err != nil {
				slog.Warn("radius: boost disconnect failed", "user", s.Username, "nas", s.NasIp, "err", err)
				continue // try again next run
			}
			given[s.ID] = on
		}
		for id := range given {
			if !open[id] {
				delete(given, id)
			}
		}
		return nil
	}
}
