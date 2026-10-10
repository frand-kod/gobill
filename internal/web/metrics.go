package web

// GET /metrics: Prometheus text format, bearer token from settings metrics_token. Mounted outside
// the session and maintenance chain, like /health. Disabled (404) while the token is empty.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/job"
	"github.com/frand-kod/gobill/internal/metrics"
)

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	token := st["metrics_token"]
	if token == "" {
		http.NotFound(w, r)
		return
	}
	got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gobill"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	fams := append(metrics.Families(), s.computedFamilies(r)...)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	metrics.Expose(w, fams)
}

// gauge is a one-sample family without labels.
func gauge(name, help string, v float64) metrics.Family {
	return metrics.Family{Name: name, Type: "gauge", Help: help, Samples: []metrics.Sample{{Value: v}}}
}

// computedFamilies are the values read at scrape time: process, storage, sessions and locks.
func (s *Server) computedFamilies(r *http.Request) []metrics.Family {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fams := []metrics.Family{
		{Name: "build_info", Type: "gauge", Help: "Build information; always 1.",
			Samples: []metrics.Sample{{Labels: metrics.Label("version", s.Version), Value: 1}}},
		gauge("process_start_time_seconds", "Unix time the process started.", float64(metrics.Started().Unix())),
		gauge("uptime_seconds", "Seconds since the process started.", time.Since(metrics.Started()).Seconds()),
		gauge("go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.", float64(ms.HeapAlloc)),
		gauge("go_memstats_sys_bytes", "Bytes obtained from the OS.", float64(ms.Sys)),
		gauge("go_goroutines", "Number of goroutines.", float64(runtime.NumGoroutine())),
		gauge("login_locked_keys", "Login keys (IPs and usernames) over the failure limit now.", float64(s.lockedCount())),
	}
	if n, err := s.openRadiusSessions(r); err == nil {
		fams = append(fams, gauge("radius_open_sessions", "Open RADIUS sessions (updated in the last 10 minutes).", float64(n)))
	}
	if s.DBPath != "" {
		dbSize, walSize := fileSize(s.DBPath), fileSize(s.DBPath+"-wal")
		fams = append(fams, gauge("db_size_bytes", "Size of the SQLite database file.", float64(dbSize)),
			gauge("db_wal_size_bytes", "Size of the SQLite WAL file.", float64(walSize)))
		if free, err := job.DiskFreeMB(filepath.Dir(s.DBPath)); err == nil {
			fams = append(fams, gauge("disk_free_bytes", "Free space available to the app on the database folder.", float64(free)*(1<<20)))
		}
	}
	return fams
}

// openRadiusSessions counts sessions that are open and were updated in the last 10 minutes (staleAfter in package radius).
func (s *Server) openRadiusSessions(r *http.Request) (int64, error) {
	var n int64
	err := s.conn.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM radius_sessions WHERE stopped_at IS NULL AND updated_at >= ?",
		time.Now().Unix()-600).Scan(&n)
	return n, err
}

// metricsTokenNew makes a new random /metrics token (SuperAdmin). The settings page shows it once.
func (s *Server) metricsTokenNew(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 32)
	rand.Read(b) // never returns an error in Go 1.24+
	tok := hex.EncodeToString(b)
	if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: "metrics_token", Value: tok}); err != nil {
		s.fail(w, "metrics token", err)
		return
	}
	s.logActivity(r, "metrics.token_new", "")
	s.sessions.Put(r.Context(), "metrics_token_once", tok)
	http.Redirect(w, r, "/admin/settings/integrations", http.StatusSeeOther)
}

// metricsTokenOff clears the token, so /metrics answers 404 (SuperAdmin).
func (s *Server) metricsTokenOff(w http.ResponseWriter, r *http.Request) {
	if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: "metrics_token", Value: ""}); err != nil {
		s.fail(w, "metrics token off", err)
		return
	}
	s.logActivity(r, "metrics.token_off", "")
	s.flashTo(w, r, "/admin/settings/integrations", "Monitoring /metrics disabled")
}

// fileSize is the size of path in bytes, 0 when it cannot be read.
func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}
