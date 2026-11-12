package reports

import (
	"testing"
	"time"
)

// A stalled job is red, and a page where everything is in order is green.
func TestStatusLevels(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	green := statusLevels{App: levelOK, Storage: levelOK, Radius: levelOK, Jobs: levelOK,
		Notify: levelOK, Pay: levelOK, Sec: levelOK, Backup: levelOK}
	ok := statusData{DaysToFull: -1, Accepted24h: 100, Rejected24h: 5,
		Jobs:   []jobRow{{Name: expiryJob, at: now.Add(-time.Minute)}},
		Backup: backupRow{at: now.Add(-time.Hour)}}
	if got := levelsOf(ok, now); got != green {
		t.Fatalf("all ok: got %+v, want %+v", got, green)
	}
	stalled := ok
	stalled.Jobs = []jobRow{{Name: expiryJob, at: now.Add(-16 * time.Minute)}}
	if got := levelsOf(stalled, now).Jobs; got != levelBad {
		t.Fatalf("expiry job last run 16 min ago: jobs level %q, want bad", got)
	}
	// router removals the retry job still owes raise the Jobs level to warn, and nothing else
	pending := ok
	pending.RouterPending = 2
	if got := levelsOf(pending, now); got.Jobs != levelWarn || got.Radius != levelOK || got.Storage != levelOK {
		t.Fatalf("2 pending router removals: %+v, want jobs warn and the rest green", got)
	}
	pending.Jobs = []jobRow{{Name: expiryJob, at: now.Add(-16 * time.Minute)}}
	if got := levelsOf(pending, now).Jobs; got != levelBad {
		t.Fatalf("pending removals must not lower a red jobs level: got %q", got)
	}
}
