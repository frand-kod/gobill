package billing

import (
	"context"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Clock reset behind the last backup time must not hide the stale-backup alert forever.
func TestAuditOps6_StaleBackupClockBehind(t *testing.T) {
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	if err := e.s.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "backup_last_at", Value: "2030-01-01 02:00:00"}); err != nil {
		t.Fatal(err)
	}
	job := &AlertJob{S: e.s, Free: func() (int64, error) { return 1 << 20, nil },
		Now: func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local) }}
	if err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if count(drain(got), "Backup+lokal") == 0 {
		t.Fatal("no backup alert while last backup is in the future")
	}
}
