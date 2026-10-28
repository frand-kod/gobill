package web

// Admin status page (GET /admin/status), its JSON twin (GET /admin/status.json) and the sparkline helper.

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/job"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/radius"
)

const sparkPoints = 96 // 24 h as 15-minute buckets

type statusData struct {
	Version    string  `json:"version"`
	Started    string  `json:"started"`
	Uptime     string  `json:"uptime"`
	HeapMB     float64 `json:"heap_mb"`
	SysMB      float64 `json:"sys_mb"`
	Goroutines int     `json:"goroutines"`
	MetricsOn  bool    `json:"metrics_enabled"`
	KeyFP      string  `json:"-"`

	DBMB       float64 `json:"db_mb"`
	WALMB      float64 `json:"wal_mb"`
	DiskFreeMB int64   `json:"disk_free_mb"`
	GrowthMB   float64 `json:"growth_mb_per_day"` // 0 when there are fewer than two daily samples
	DaysToFull float64 `json:"days_to_full"`      // -1 when unknown or not growing
	HistDays   int     `json:"history_days"`

	Accepted24h float64      `json:"radius_accepted_24h"`
	Rejected24h float64      `json:"radius_rejected_24h"`
	AvgMs       float64      `json:"radius_avg_ms"` // since start
	Open        int64        `json:"radius_open_sessions"`
	NAS         []nasRow     `json:"nas"`
	Jobs        []jobRow     `json:"jobs"`
	Notify      []notifyRow  `json:"notifications"`
	PayOK       float64      `json:"payment_callbacks_ok"`
	PayFailed   float64      `json:"payment_callbacks_failed"`
	PayLast     string       `json:"payment_last_callback"`
	Sec         secRow       `json:"security"`
	Backup      backupRow    `json:"backup"`
	Levels      statusLevels `json:"levels"`
	JobsFailing int          `json:"-"` // jobs with a failure in a row, for the summary row

	RadiusAcc spark `json:"-"`
	RadiusRej spark `json:"-"`
	NotifyErr spark `json:"-"`
	LoginFail spark `json:"-"`
}

type nasRow struct {
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Seen    bool   `json:"seen"`
	Minutes int64  `json:"minutes_since_packet"`
	Warn    bool   `json:"warn"`
	// BadSecret: a bad-secret packet came in the last hour; BadCount is the total since start.
	BadSecret bool  `json:"bad_secret"`
	BadCount  int64 `json:"bad_secret_total"`
}

type jobRow struct {
	Name     string `json:"name"`
	LastRun  string `json:"last_run"`
	DurMs    int64  `json:"duration_ms"`
	LastErr  string `json:"last_error"`
	Failures int    `json:"failures"`
	at       time.Time
}

type notifyRow struct {
	Name    string  `json:"channel"`
	Sent    float64 `json:"sent"`
	Failed  float64 `json:"failed"`
	Streak  int     `json:"streak"`
	LastErr string  `json:"last_error"`
	LastAt  string  `json:"last_at"`
}

type secRow struct {
	Fail24h   float64 `json:"login_failures_24h"`
	Fail10m   float64 `json:"login_failures_10m"`
	TwoFA     float64 `json:"twofa_failures"`
	Locked    int     `json:"locked_keys"`
	Forbidden float64 `json:"radius_php_forbidden"`
}

type backupRow struct {
	LocalAt    string `json:"local_at"`
	LocalFile  string `json:"local_file"`
	LocalStale bool   `json:"local_stale"`
	Mirror     string `json:"mirror"`
	MirrorAt   string `json:"mirror_at"`
	MirrorErr  string `json:"mirror_error"`
	at         time.Time
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	d, err := s.statusData(r)
	if err != nil {
		s.fail(w, "status", err)
		return
	}
	s.render(w, r, http.StatusOK, "status", Page{Title: "System Status", Data: d, Warn: strings.TrimSpace(s.proxyWarning(r) + " " + s.keyWarning(r))})
}

func (s *Server) statusJSON(w http.ResponseWriter, r *http.Request) {
	d, err := s.statusData(r)
	if err != nil {
		s.fail(w, "status json", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(d)
}

// statusData gathers everything the status page shows. The metrics are in process memory, so
// counts are since start unless the label says 24 h.
func (s *Server) statusData(r *http.Request) (statusData, error) {
	ctx := r.Context()
	st, err := s.loadSettings(ctx)
	if err != nil {
		return statusData{}, err
	}
	loc, now := s.location(), time.Now()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	up := now.Sub(metrics.Started())
	d := statusData{
		Version: s.Version, Started: metrics.Started().In(loc).Format(backupLayout),
		Uptime: fmt.Sprintf("%dd %02dh %02dm", int(up.Hours())/24, int(up.Hours())%24, int(up.Minutes())%60),
		HeapMB: float64(ms.HeapAlloc) / (1 << 20), SysMB: float64(ms.Sys) / (1 << 20), Goroutines: runtime.NumGoroutine(),
		MetricsOn: st["metrics_token"] != "", KeyFP: keyFingerprint(s.SecretKey), DaysToFull: -1,
		Accepted24h: metrics.Recent("radius_auth_accepted_total", 1440),
		Rejected24h: metrics.Recent("radius_auth_rejected_total", 1440),
		PayOK:       metrics.Value("payment_callbacks_total", "result", "ok"),
		PayFailed:   metrics.Value("payment_callbacks_total", "result", "failed"),
		RadiusAcc:   newSpark(metrics.Series("radius_auth_accepted_total"), now, loc),
		RadiusRej:   newSpark(metrics.Series("radius_auth_rejected_total"), now, loc),
		NotifyErr:   newSpark(metrics.Series("notifications_failed_total"), now, loc),
		LoginFail:   newSpark(metrics.Series("login_failures_total"), now, loc),
	}
	if n := metrics.Value("radius_auth_duration_seconds_count"); n > 0 {
		d.AvgMs = metrics.Value("radius_auth_duration_seconds_sum") / n * 1000
	}
	if t := metrics.Value("payment_callback_last_timestamp"); t > 0 {
		d.PayLast = time.Unix(int64(t), 0).In(loc).Format(backupLayout)
	}
	if err := s.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM radius_sessions WHERE stopped_at IS NULL AND updated_at >= ?",
		now.Unix()-radius.StaleMax()).Scan(&d.Open); err != nil {
		return d, err
	}

	// storage: file sizes, free space and the growth from the daily samples
	free, _ := job.DiskFreeMB(filepath.Dir(s.DBPath))
	d.DBMB, d.WALMB = fileMB(s.DBPath), fileMB(s.DBPath+"-wal")
	d.DiskFreeMB = free
	var hist []billing.DBSample
	_ = json.Unmarshal([]byte(st[billing.DBHistoryKey]), &hist)
	d.HistDays = len(hist)
	if len(hist) >= 2 {
		a, b := hist[0], hist[len(hist)-1]
		ta, e1 := time.Parse("2006-01-02", a.Day)
		tb, e2 := time.Parse("2006-01-02", b.Day)
		if e1 == nil && e2 == nil && tb.After(ta) {
			days := tb.Sub(ta).Hours() / 24
			d.GrowthMB = float64(b.Size-a.Size) / (1 << 20) / days
			if d.GrowthMB > 0 && free > 0 {
				d.DaysToFull = float64(free) / d.GrowthMB
			}
		}
	}

	silent := 15
	fmt.Sscanf(st["alert_nas_silent_minutes"], "%d", &silent)
	nas, err := s.queries.ListNAS(ctx)
	if err != nil {
		return d, err
	}
	for _, n := range nas {
		row := nasRow{Name: n.Name, IP: n.Ip}
		if ts := metrics.Value("radius_nas_last_packet_timestamp", "nas", n.Ip); ts > 0 {
			row.Seen = true
			row.Minutes = int64(now.Sub(time.Unix(int64(ts), 0)).Minutes())
			row.Warn = row.Minutes >= int64(silent)
		}
		if ts := metrics.Value("radius_bad_secret_last_timestamp", "nas", n.Ip); ts > 0 && now.Sub(time.Unix(int64(ts), 0)) < time.Hour {
			row.BadSecret = true
		}
		row.BadCount = int64(metrics.Value("radius_bad_secret_total", "nas", n.Ip))
		d.NAS = append(d.NAS, row)
	}

	for _, j := range job.Stats() {
		d.Jobs = append(d.Jobs, jobRow{Name: j.Name, LastRun: j.LastRun.In(loc).Format(backupLayout),
			DurMs: j.Duration.Milliseconds(), LastErr: j.LastErr, Failures: j.Failures, at: j.LastRun})
		if j.Failures > 0 {
			d.JobsFailing++
		}
	}

	byName := map[string]notify.ChannelState{}
	for _, c := range notify.Channels() {
		byName[c.Name] = c
	}
	for _, name := range []string{"wa", "sms", "email", "telegram", "webhook"} {
		c := byName[name]
		row := notifyRow{Name: name, Sent: c.Sent, Failed: c.Failed, Streak: c.Streak, LastErr: c.LastErr}
		if !c.LastAt.IsZero() {
			row.LastAt = c.LastAt.In(loc).Format(backupLayout)
		}
		d.Notify = append(d.Notify, row)
	}

	d.Sec = secRow{
		Fail24h:   metrics.Recent("login_failures_total", 1440),
		Fail10m:   metrics.Recent("login_failures_total", 10),
		TwoFA:     metrics.Value("login_failures_total", "scope", "2fa"),
		Locked:    s.lockedCount(),
		Forbidden: metrics.Value("radius_rest_forbidden_total"),
	}

	d.Backup = backupRow{LocalAt: st["backup_last_at"], LocalFile: st["backup_last_file"], Mirror: s.BackupMirror,
		MirrorAt: st["backup_mirror_at"], MirrorErr: st["backup_mirror_error"]}
	if t, err := time.ParseInLocation(backupLayout, d.Backup.LocalAt, loc); d.Backup.LocalAt != "" && err == nil {
		d.Backup.LocalStale = now.Sub(t) > backupStaleAge
		d.Backup.at = t
	}
	d.Levels = levelsOf(d, now)
	return d, nil
}

// backupLayout is the timestamp layout used for every time shown on the status page.
const backupLayout = "2006-01-02 15:04:05"

func fileMB(path string) float64 {
	return float64(fileSize(path)) / (1 << 20)
}

// bucketSums folds the per-minute series into sparkPoints buckets.
func bucketSums(v []float64) []float64 {
	per := len(v) / sparkPoints
	out := make([]float64, sparkPoints)
	for i, x := range v {
		out[i/per] += x
	}
	return out
}

// sparkBar is one bar of a sparkline: its height in percent of the tallest bar, and its tooltip.
type sparkBar struct {
	Pct   string
	Title string
}

// spark is a 24 h series as sparkPoints bars, oldest first, with the total of the series.
type spark struct {
	Bars  []sparkBar
	Total float64
}

// newSpark folds the per-minute series into bars. Each tooltip names the start of its 15 minutes.
func newSpark(series []float64, now time.Time, loc *time.Location) spark {
	vals := bucketSums(series)
	top := 0.0
	for _, v := range vals {
		top = math.Max(top, v)
	}
	per := len(series) / sparkPoints
	first := now.Truncate(time.Minute).Add(-time.Duration(len(series)-1) * time.Minute)
	var sp spark
	for i, v := range vals {
		pct := 0.0
		if top > 0 {
			pct = v / top * 100
		}
		start := first.Add(time.Duration(i*per) * time.Minute).In(loc)
		sp.Bars = append(sp.Bars, sparkBar{Pct: fmt.Sprintf("%.1f", pct), Title: fmt.Sprintf("%s: %.0f", start.Format("2006-01-02 15:04"), v)})
		sp.Total += v
	}
	return sp
}

// statusLevel is the colour of a section on the status page: ok (green), warn (amber) or bad (red).
type statusLevel string

const (
	levelOK   statusLevel = "ok"
	levelWarn statusLevel = "warn"
	levelBad  statusLevel = "bad"
)

// Dot is the colour class of the status dot for the level.
func (l statusLevel) Dot() string {
	switch l {
	case levelBad:
		return "bg-err-fg"
	case levelWarn:
		return "bg-warn-fg"
	}
	return "bg-ok-fg"
}

// statusLevels has one level per section, in the order of the summary row.
type statusLevels struct {
	App     statusLevel `json:"app"`
	Storage statusLevel `json:"storage"`
	Radius  statusLevel `json:"radius"`
	Jobs    statusLevel `json:"jobs"`
	Notify  statusLevel `json:"notifications"`
	Pay     statusLevel `json:"payments"`
	Sec     statusLevel `json:"security"`
	Backup  statusLevel `json:"backup"`
}

// Thresholds of the level rules in levelsOf.
const (
	expiryJob         = "expiry" // runs every minute; a last run older than stalledJobAge means it has stopped
	stalledJobAge     = 15 * time.Minute
	radiusRejectRatio = 10 // amber when rejected(24 h) > accepted(24 h) / radiusRejectRatio
	diskBadDays       = 7  // red when the disk is projected to fill in fewer days
	diskWarnDays      = 30 // amber when it is projected to fill in fewer days
	backupStaleAge    = 36 * time.Hour // same age as the "Older than 36 hours" badge
)

// levelsOf sets the level of each section from the data on the status page. Rules that need data the
// page does not have are left out: backup is never red (the encryption key reminder is not shown here),
// and a NAS that has not sent a packet since start is not red. Application has no error fields: green.
func levelsOf(d statusData, now time.Time) statusLevels {
	l := statusLevels{App: levelOK, Storage: levelOK, Radius: levelOK, Jobs: levelOK,
		Notify: levelOK, Pay: levelOK, Sec: levelOK, Backup: levelOK}
	for _, j := range d.Jobs {
		if j.Failures > 0 || (j.Name == expiryJob && now.Sub(j.at) > stalledJobAge) {
			l.Jobs = levelBad
		}
	}
	for _, n := range d.NAS {
		if n.BadSecret || n.Warn {
			l.Radius = levelBad
		}
	}
	if l.Radius == levelOK && d.Rejected24h > d.Accepted24h/radiusRejectRatio {
		l.Radius = levelWarn
	}
	// Sent and Failed per channel are counts since start, so a channel that never sent is red
	// only when it has failed; the amber rule uses the 24 h failure series.
	if d.NotifyErr.Total > 0 {
		l.Notify = levelWarn
	}
	for _, c := range d.Notify {
		if c.Failed > 0 && c.Sent == 0 {
			l.Notify = levelBad
		}
	}
	if d.Sec.Fail10m > 0 || d.Sec.Locked > 0 {
		l.Sec = levelWarn
	}
	if d.DaysToFull >= 0 {
		switch {
		case d.DaysToFull < diskBadDays:
			l.Storage = levelBad
		case d.DaysToFull < diskWarnDays:
			l.Storage = levelWarn
		}
	}
	if d.Backup.at.IsZero() || now.Sub(d.Backup.at) > backupStaleAge {
		l.Backup = levelWarn
	}
	if d.PayFailed > 0 {
		l.Pay = levelWarn
	}
	return l
}
