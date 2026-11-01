package radius

import (
	"context"
	"log/slog"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// authLogField caps the client-chosen strings (user name, MAC) stored per attempt.
const authLogField = 128

// logReason is the short class of a reject message, "" for an accept. Same classes as the
// radius_auth_rejected_total metric.
func logReason(msg string) string {
	switch msg {
	case "":
		return ""
	case badPassword:
		return "bad_password"
	}
	return rejectReason(msg)
}

// logAuth records one Access-Request decision in radius_auth_log (the post-auth log). Test logins
// (DryRun) are not logged. The write goes through the accounting
// group commit in the background, so the Access-Accept does not wait for the disk.
// ponytail: a spray of rejects fills the table until the daily pruning (log_keep_days); add sampling if that bites.
func (s *Server) logAuth(ctx context.Context, rq AuthRequest, d Decision) {
	if rq.DryRun {
		return
	}
	cut := func(x string) string { return x[:min(len(x), authLogField)] }
	arg := db.InsertRadiusAuthLogParams{At: s.now().Unix(), Username: cut(rq.User), NasIp: rq.NAS, Mac: cut(rq.MAC),
		Reason: logReason(d.Reject), Message: cut(d.Reject)}
	if d.Reject == "" {
		arg.Accepted = 1
	}
	write := func(ctx context.Context) {
		if err := s.commit(ctx, func(q *db.Queries) error { return q.InsertRadiusAuthLog(ctx, arg) }); err != nil {
			slog.Warn("radius: write auth log", "err", err)
		}
	}
	if s.DB == nil { // tests and the REST fallback server: inline
		write(ctx)
		return
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		write(c)
	}()
}
