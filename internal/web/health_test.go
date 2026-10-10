package web

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	s, _ := newTestApp(t)
	s.DBPath = filepath.Join(t.TempDir(), "nuxbill.db")
	h := s.Handler()

	w := do(h, "GET", "/health", nil, nil)
	wantCode(t, w, http.StatusOK, "health ok")
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "ok" || out["db"] != "ok" || len(out) != 2 {
		t.Fatalf("health body = %v, want only status and db", out)
	}

	s.conn.Close()
	w = do(h, "GET", "/health", nil, nil)
	wantCode(t, w, http.StatusServiceUnavailable, "health with closed db")
	if got := w.Body.String(); !strings.Contains(got, `"status":"down"`) || strings.Contains(got, s.DBPath) {
		t.Fatalf("closed db body = %s", got)
	}
}
