package billing

// Operator alerts about the box itself: disk, silent NAS, failing jobs and notification channels,
// login brute force, stale backups, and restarts after an unclean stop.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/job"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/notify"
)

const (
	diskLowMB        = 200 // free space below which the operator is alerted; /health degrades at the same level
	jobFailMax       = 3   // consecutive failed runs of one job
	chanFailMax      = 5   // consecutive failed sends on one channel
	bruteMax         = 30  // login failures in bruteWindow minutes
	bruteWindow      = 10
	backupMaxAge     = 36 * time.Hour
	backupTime       = "2006-01-02 15:04:05" // layout of the backup_* settings, as written by job.Backup
	dbHistoryKeep    = 30
	DBHistoryKey     = "metrics_db_size_history" // settings key: JSON array of DBSample, oldest first
	alertTimeout     = 20 * time.Second
	defaultNASSilent = 15 // minutes, alert_nas_silent_minutes
)

// DBSample is one daily reading of the database file size.
type DBSample struct {
	Day  string `json:"day"` // local date, 2006-01-02
	Size int64  `json:"size"`
}

// AlertJob evaluates the alert rules every minute. Each rule alerts once when it turns bad and
// once when it recovers; it stays silent while it remains bad. The state is in memory, so a
// restart re-alerts for conditions that are still bad.
type AlertJob struct {
	S      *Service
	DBPath string                // SQLite file, for the daily size sample; "" skips the sample
	Free   func() (int64, error) // free MB on the database folder
	Now    func() time.Time      // nil = time.Now
	active map[string]bool
}

// Run is the job.Run body.
func (a *AlertJob) Run(ctx context.Context) error {
	if a.active == nil {
		a.active = map[string]bool{}
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	st := settingsMap(ctx, a.S.Q)
	var errs []error

	mb, err := a.Free()
	if err != nil {
		errs = append(errs, err)
	} else {
		a.edge(ctx, "disk", mb < diskLowMB,
			fmt.Sprintf("Disk hampir penuh: tinggal %d MB di folder database", mb),
			fmt.Sprintf("Disk pulih: %d MB tersisa di folder database", mb))
	}

	a.checkNAS(ctx, st, now)

	for _, j := range job.Stats() {
		a.edge(ctx, "job:"+j.Name, j.Failures >= jobFailMax,
			fmt.Sprintf("Job %s gagal %d kali berturut-turut: %s", j.Name, j.Failures, j.LastErr),
			fmt.Sprintf("Job %s berjalan normal lagi", j.Name))
	}
	for _, c := range notify.Channels() {
		a.edge(ctx, "chan:"+c.Name, c.Streak >= chanFailMax,
			fmt.Sprintf("Kanal notifikasi %s gagal %d kali berturut-turut: %s", c.Name, c.Streak, c.LastErr),
			fmt.Sprintf("Kanal notifikasi %s pulih", c.Name))
	}

	n := metrics.Recent("login_failures_total", bruteWindow)
	a.edge(ctx, "brute", n > bruteMax,
		fmt.Sprintf("Kegagalan login %d kali dalam %d menit terakhir (admin, portal, 2FA): kemungkinan tebak sandi", int(n), bruteWindow),
		"Kegagalan login sudah mereda")

	a.edge(ctx, "backup:mirror", st["backup_mirror_error"] != "",
		"Backup mirror gagal: "+st["backup_mirror_error"],
		"Backup mirror kembali normal")
	stale := false
	last := st["backup_last_at"]
	if t, err := time.ParseInLocation(backupTime, last, now.Location()); last != "" && err == nil {
		stale = now.Sub(t) > backupMaxAge
	}
	a.edge(ctx, "backup:local", stale,
		"Backup lokal terakhir "+last+", lebih dari 36 jam lalu",
		"Backup lokal kembali berjalan")

	if err := a.sampleDB(ctx, st, now); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// checkNAS alerts when a NAS IP that has sent RADIUS packets since start sends none for
// alert_nas_silent_minutes. The nas table only supplies a display name. The samples are copied
// first: metrics.Each holds the registry lock.
func (a *AlertJob) checkNAS(ctx context.Context, st map[string]string, now time.Time) {
	silent, err := strconv.Atoi(st["alert_nas_silent_minutes"])
	if err != nil || silent < 1 {
		silent = defaultNASSilent
	}
	nas, err := a.S.Q.ListNAS(ctx)
	if err != nil {
		slog.Error("alert: list nas", "err", err)
		return
	}
	type seen struct {
		ip string
		at time.Time
	}
	var last []seen
	metrics.Each("radius_nas_last_packet_timestamp", func(kv []string, v float64) {
		if len(kv) == 2 && v > 0 {
			last = append(last, seen{kv[1], time.Unix(int64(v), 0)})
		}
	})
	live := map[string]bool{}
	for _, p := range last {
		live["nas:"+p.ip] = true
		name := p.ip // any NAS that sent a packet since start; the nas table name is only a label
		for _, n := range nas {
			if n.Ip == p.ip {
				name = n.Name
			}
		}
		a.edge(ctx, "nas:"+p.ip, now.Sub(p.at) >= time.Duration(silent)*time.Minute,
			fmt.Sprintf("NAS %s (%s) tidak mengirim paket RADIUS selama %d menit", name, p.ip, silent),
			fmt.Sprintf("NAS %s (%s) kembali mengirim paket RADIUS", name, p.ip))
	}
	// A NAS whose series the metrics registry evicted (older than 24 h or over the cap) is forgotten
	// silently: no alert, so a NAS that returns later starts a fresh episode.
	for k := range a.active {
		if strings.HasPrefix(k, "nas:") && !live[k] {
			delete(a.active, k)
		}
	}
}

// edge sends onMsg when rule key turns bad and offMsg when it recovers. Nothing is sent while the state holds.
func (a *AlertJob) edge(ctx context.Context, key string, bad bool, onMsg, offMsg string) {
	if bad == a.active[key] {
		return
	}
	a.active[key] = bad
	msg := offMsg
	if bad {
		msg = onMsg
	}
	actx, cancel := context.WithTimeout(ctx, alertTimeout)
	defer cancel()
	if err := a.S.Alert(actx, msg); err != nil {
		slog.Error("operator alert", "rule", key, "err", err)
	}
}

// sampleDB stores today's database size once a day, keeping the last dbHistoryKeep days.
func (a *AlertJob) sampleDB(ctx context.Context, st map[string]string, now time.Time) error {
	if a.DBPath == "" {
		return nil
	}
	day := now.Format("2006-01-02")
	var hist []DBSample
	_ = json.Unmarshal([]byte(st[DBHistoryKey]), &hist)
	if len(hist) > 0 && hist[len(hist)-1].Day == day {
		return nil
	}
	fi, err := os.Stat(a.DBPath)
	if err != nil {
		return err
	}
	hist = append(hist, DBSample{Day: day, Size: fi.Size()})
	if len(hist) > dbHistoryKeep {
		hist = hist[len(hist)-dbHistoryKeep:]
	}
	b, _ := json.Marshal(hist)
	return a.S.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: DBHistoryKey, Value: string(b)})
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
