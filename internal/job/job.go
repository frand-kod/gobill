// Package job holds the scheduled background jobs.
package job

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/metrics"
)

// State is the last run of one job, for the status page and the job alert rule.
type State struct {
	Name     string
	LastRun  time.Time
	Duration time.Duration
	LastErr  string
	Failures int // consecutive failed runs
}

var (
	stateMu sync.Mutex
	states  = map[string]*State{}
)

// Stats returns the state of every job that has run, sorted by name.
func Stats() []State {
	stateMu.Lock()
	defer stateMu.Unlock()
	out := make([]State, 0, len(states))
	for _, s := range states {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func record(name string, start time.Time, err error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	s := states[name]
	if s == nil {
		s = &State{Name: name}
		states[name] = s
	}
	s.LastRun, s.Duration = start, time.Since(start)
	s.LastErr = ""
	if err != nil {
		s.LastErr, s.Failures = err.Error(), s.Failures+1
	} else {
		s.Failures = 0
	}
	if err != nil {
		metrics.Inc("job_runs_total", "job", name, "result", "error")
	} else {
		metrics.Inc("job_runs_total", "job", name, "result", "ok")
	}
}

// Run calls fn every interval until ctx is done. Errors are logged, not fatal.
func Run(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			start := time.Now()
			err := fn(ctx)
			record(name, start, err)
			if err != nil {
				slog.Error("job failed", "job", name, "err", err)
			}
		}
	}
}
