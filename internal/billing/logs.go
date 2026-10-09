package billing

// Log cleanup and its scheduled job.

import (
	"database/sql"

	"context"
	"fmt"
	"strconv"
)

// LogKinds are the log tables CleanLog knows.
var LogKinds = []string{"activity", "radius", "messages"}

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
		return s.Q.DeleteRadiusSessionsClosedBefore(ctx, sql.NullInt64{Int64: cut, Valid: true})
	case "messages":
		return s.Q.DeleteMessageLogsBefore(ctx, cut)
	}
	return 0, fmt.Errorf("unknown log kind %q", kind)
}

// LogCleanJob is the daily auto-clean (run it every few minutes; it acts once per day). It reads
// the setting log_keep_days; 0 or empty = keep forever.
func (s *Service) LogCleanJob(trusted func() bool) func(context.Context) error {
	var last string
	return func(ctx context.Context) error {
		day := s.now().Format("2006-01-02")
		if day == last || trusted != nil && !trusted() {
			return nil
		}
		days, _ := strconv.Atoi(setting(ctx, s.Q, "log_keep_days"))
		for _, k := range LogKinds {
			if _, err := s.CleanLog(ctx, k, days); err != nil {
				return err
			}
		}
		last = day
		return nil
	}
}
