package billing

// Log cleanup and its scheduled job.

import (
	"database/sql"

	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// LogKinds are the log tables CleanLog knows.
var LogKinds = []string{"activity", "radius", "messages"}

// LogKeepDefault is the retention when log_keep_days is not set; "0" still means keep forever.
const LogKeepDefault = 90

// radiusStaleAfter closes an open RADIUS session that has had no update for this long, at its last update.
// ponytail: fixed 1 h instead of 3 x the NAS interim interval (no such setting); add one if NAS intervals differ.
const radiusStaleAfter = time.Hour

// CleanLog deletes rows of one log kind older than days and returns how many. For radius only
// CLOSED sessions are removed: open ones are live data. days <= 0 does nothing.
func (s *Service) CleanLog(ctx context.Context, kind string, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	cut := s.now().AddDate(0, 0, -days).Unix()
	switch kind {
	case "activity":
		return s.Q.DeleteActivityLogsBefore(ctx, cut)
	case "radius":
		if _, err := s.Q.DeleteRadiusUsageDailyBefore(ctx, time.Unix(cut, 0).In(s.Location()).Format("2006-01-02")); err != nil {
			return 0, err
		}
		return s.Q.DeleteRadiusSessionsClosedBefore(ctx, sql.NullInt64{Int64: cut, Valid: true})
	case "messages":
		return s.Q.DeleteMessageLogsBefore(ctx, cut)
	}
	return 0, fmt.Errorf("unknown log kind %q", kind)
}

// LogCleanJob runs every few minutes. Each tick closes stale open RADIUS sessions; once a day it also
// cleans the logs, prunes read inbox messages and unpaid payment requests older than log_keep_days,
// and runs PRAGMA optimize. log_keep_days: missing = 90 days, 0 = keep forever.
func (s *Service) LogCleanJob(trusted func() bool) func(context.Context) error {
	var last string
	return func(ctx context.Context) error {
		if trusted != nil && !trusted() {
			return nil
		}
		if _, err := s.Q.CloseStaleRadiusSessions(ctx, s.now().Add(-radiusStaleAfter).Unix()); err != nil {
			return err
		}
		day := s.now().Format("2006-01-02")
		if day == last {
			return nil
		}
		days := LogKeepDefault
		if v := setting(ctx, s.Q, "log_keep_days"); v != "" {
			days, _ = strconv.Atoi(v)
		}
		for _, k := range LogKinds {
			if _, err := s.CleanLog(ctx, k, days); err != nil {
				return err
			}
		}
		if days > 0 {
			cut := s.now().AddDate(0, 0, -days).Unix()
			if err := drainBatches(func() (int64, error) { return s.Q.DeleteReadInboxBefore(ctx, cut) }); err != nil {
				return err
			}
			if err := drainBatches(func() (int64, error) { return s.Q.DeleteUnpaidPaymentRequestsBefore(ctx, cut) }); err != nil {
				return err
			}
		}
		if err := db.Optimize(ctx, s.DB, "PRAGMA optimize"); err != nil {
			return err
		}
		last = day
		return nil
	}
}

// drainBatches runs one batch delete (at most 5000 rows) until it removes nothing, so a big backlog never holds the write lock for long.
func drainBatches(batch func() (int64, error)) error {
	for {
		n, err := batch()
		if err != nil || n == 0 {
			return err
		}
	}
}
