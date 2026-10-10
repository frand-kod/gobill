package web

// GET /health for uptime monitors. No session, no settings, no counts.

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/job"
)

// healthLowMB is the free space below which status becomes "degraded".
const healthLowMB = 200

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	out := map[string]any{"status": "ok", "db": "ok", "disk_free_mb": nil, "version": s.Version}
	code := http.StatusOK
	var one int
	if err := s.conn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		out["status"], out["db"] = "down", strings.ReplaceAll(err.Error(), s.DBPath, "<db>")
		code = http.StatusServiceUnavailable
	}
	if free, err := job.DiskFreeMB(filepath.Dir(s.DBPath)); err == nil {
		out["disk_free_mb"] = free
		if free < healthLowMB && out["status"] == "ok" {
			out["status"] = "degraded"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(out)
}
