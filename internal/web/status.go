package web

// Admin status page (GET /admin/status), its JSON twin (GET /admin/status.json) and the sparkline helper.

import (
	"encoding/json"
	"fmt"
	"html/template"
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

	DBMB       float64 `json:"db_mb"`
	WALMB      float64 `json:"wal_mb"`
	DiskFreeMB int64   `json:"disk_free_mb"`
	GrowthMB   float64 `json:"growth_mb_per_day"` // 0 when there are fewer than two daily samples
	DaysToFull float64 `json:"days_to_full"`      // -1 when unknown or not growing
	HistDays   int     `json:"history_days"`

	Accepted24h float64     `json:"radius_accepted_24h"`
	Rejected24h float64     `json:"radius_rejected_24h"`
	AvgMs       float64     `json:"radius_avg_ms"` // since start
	Open        int64       `json:"radius_open_sessions"`
	NAS         []nasRow    `json:"nas"`
	Jobs        []jobRow    `json:"jobs"`
	Notify      []notifyRow `json:"notifications"`
	PayOK       float64     `json:"payment_callbacks_ok"`
	PayFailed   float64     `json:"payment_callbacks_failed"`
	PayLast     string      `json:"payment_last_callback"`
	Sec         secRow      `json:"security"`
	Backup      backupRow   `json:"backup"`

	RadiusAcc []float64 `json:"-"`
	RadiusRej []float64 `json:"-"`
	NotifyErr []float64 `json:"-"`
	LoginFail []float64 `json:"-"`
}

type nasRow struct {
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Seen    bool   `json:"seen"`
	Minutes int64  `json:"minutes_since_packet"`
	Warn    bool   `json:"warn"`
}

type jobRow struct {
	Name     string `json:"name"`
	LastRun  string `json:"last_run"`
	DurMs    int64  `json:"duration_ms"`
	LastErr  string `json:"last_error"`
	Failures int    `json:"failures"`
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
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	d, err := s.statusData(r)
	if err != nil {
		s.fail(w, "status", err)
		return
	}
	s.render(w, r, http.StatusOK, "status", Page{Title: "System Status", Data: d})
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
		MetricsOn: st["metrics_token"] != "", DaysToFull: -1,
		Accepted24h: metrics.Recent("radius_auth_accepted_total", 1440),
		Rejected24h: metrics.Recent("radius_auth_rejected_total", 1440),
		PayOK:       metrics.Value("payment_callbacks_total", "result", "ok"),
		PayFailed:   metrics.Value("payment_callbacks_total", "result", "failed"),
		RadiusAcc:   bucketSums(metrics.Series("radius_auth_accepted_total")),
		RadiusRej:   bucketSums(metrics.Series("radius_auth_rejected_total")),
		NotifyErr:   bucketSums(metrics.Series("notifications_failed_total")),
		LoginFail:   bucketSums(metrics.Series("login_failures_total")),
	}
	if n := metrics.Value("radius_auth_duration_seconds_count"); n > 0 {
		d.AvgMs = metrics.Value("radius_auth_duration_seconds_sum") / n * 1000
	}
	if t := metrics.Value("payment_callback_last_timestamp"); t > 0 {
		d.PayLast = time.Unix(int64(t), 0).In(loc).Format(backupLayout)
	}
	if err := s.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM radius_sessions WHERE stopped_at IS NULL AND updated_at >= ?",
		now.Unix()-600).Scan(&d.Open); err != nil {
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
		d.NAS = append(d.NAS, row)
	}

	for _, j := range job.Stats() {
		d.Jobs = append(d.Jobs, jobRow{Name: j.Name, LastRun: j.LastRun.In(loc).Format(backupLayout),
			DurMs: j.Duration.Milliseconds(), LastErr: j.LastErr, Failures: j.Failures})
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
		d.Backup.LocalStale = now.Sub(t) > 36*time.Hour
	}
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

// sparkline draws values as an inline SVG polyline in currentColor, so the caller sets the colour
// with a text class and it follows the light and dark theme. The values are numbers only.
func sparkline(vals []float64) template.HTML {
	top := 0.0
	for _, v := range vals {
		top = math.Max(top, v)
	}
	var pts []string
	for i, v := range vals {
		y := 30.0
		if top > 0 {
			y = 30 - v/top*28
		}
		pts = append(pts, fmt.Sprintf("%d,%.1f", i, y+1))
	}
	return template.HTML(`<svg class="h-12 w-full text-info-fg" viewBox="0 0 96 32" preserveAspectRatio="none" aria-hidden="true">` +
		`<polyline fill="none" stroke="currentColor" stroke-width="1.5" vector-effect="non-scaling-stroke" points="` +
		strings.Join(pts, " ") + `"/></svg>`)
}
