package radius

import (
	"context"
	"log/slog"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// fupRate is the FUP Mikrotik-Rate-Limit when the plan has a FUP and the customer used up the data
// limit, "" otherwise (no FUP, or still within the limit).
func fupRate(ctx context.Context, q *db.Queries, lr db.GetRadiusLoginRow) (string, error) {
	fup := rateLimit(lr.FupRateUp, lr.FupRateDown, lr.FupRateUpUnit, lr.FupRateDownUnit, lr.FupBurst)
	if fup == "" || lr.Limited != 1 || !lr.DataLimit.Valid || (lr.LimitType.String != "Data_Limit" && lr.LimitType.String != "Both_Limit") {
		return "", nil
	}
	bytes, _, err := db.LoginUsage(ctx, q, lr.Customer, lr.StartedAt)
	if err != nil || bytes < db.QuotaBytes(lr.DataLimit.Int64, lr.DataUnit.String, lr.QuotaCarryBytes) {
		return "", err
	}
	return fup, nil
}

// FUPJob slows down the online sessions of customers who passed their data limit, once per session:
// a CoA-Request with the FUP rate, and a Disconnect-Request when the NAS refuses it (the customer
// reconnects and Access-Accept carries the FUP rate). A new login already gets it from authorize.
// ponytail: sessions go one by one; the sent set is in memory, so a restart sends one more CoA.
func FUPJob(q *db.Queries, key []byte, port string) func(context.Context) error {
	sent := map[int64]bool{}
	return func(ctx context.Context) error {
		ss, err := q.ListFUPSessions(ctx)
		if err != nil {
			return err
		}
		open := map[int64]bool{}
		for _, s := range ss {
			open[s.ID] = true
			if sent[s.ID] {
				continue
			}
			lr, err := q.GetRadiusLogin(ctx, s.Username)
			if err != nil {
				continue
			}
			if fup, err := fupRate(ctx, q, lr); err != nil {
				return err
			} else if fup == "" {
				continue
			}
			err = CoA(ctx, q, key, port, s, "", time.Now())
			if err != nil {
				slog.Warn("radius: FUP CoA failed, disconnecting", "user", s.Username, "nas", s.NasIp, "err", err)
				err = Disconnect(ctx, q, key, port, s)
			}
			if err != nil {
				slog.Warn("radius: FUP disconnect failed", "user", s.Username, "nas", s.NasIp, "err", err)
				continue // try again next run
			}
			sent[s.ID] = true
		}
		for id := range sent {
			if !open[id] {
				delete(sent, id)
			}
		}
		return nil
	}
}
