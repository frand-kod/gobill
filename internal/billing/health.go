package billing

// Self-alerts to the operator: low disk on the database folder and restarts after an unclean stop.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// diskLowMB is the free space below which the operator is alerted; /health degrades at the same level.
const diskLowMB = 200

// DiskAlertJob returns the job body for job.Run. It alerts once when free space drops below
// diskLowMB and once when it recovers. free is injected so tests need no real disk.
func (s *Service) DiskAlertJob(free func() (int64, error)) func(context.Context) error {
	low := false
	return func(context.Context) error {
		mb, err := free()
		if err != nil {
			return err
		}
		if (mb < diskLowMB) == low {
			return nil
		}
		low = !low
		if low {
			s.telegram(fmt.Sprintf("Disk hampir penuh: tinggal %d MB di folder database", mb))
		} else {
			s.telegram(fmt.Sprintf("Disk pulih: %d MB tersisa di folder database", mb))
		}
		return nil
	}
}

// StartMarker writes the running marker at path. If one was already there, the previous run did
// not shut down cleanly, so the operator is told. The caller removes the marker on graceful shutdown.
func (s *Service) StartMarker(path string) error {
	_, statErr := os.Stat(path)
	stale := statErr == nil
	if err := os.WriteFile(path, []byte("running\n"), 0o644); err != nil {
		return err
	}
	if stale {
		slog.Warn("previous run did not stop cleanly", "marker", path)
		s.telegram("gobill dimulai ulang setelah berhenti tidak normal")
	}
	return nil
}
