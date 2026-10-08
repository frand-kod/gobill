// Package job holds the scheduled background jobs.
package job

import (
	"context"
	"log/slog"
	"time"
)

// Run calls fn every interval until ctx is done. Errors are logged, not fatal.
func Run(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := fn(ctx); err != nil {
				slog.Error("job failed", "job", name, "err", err)
			}
		}
	}
}
