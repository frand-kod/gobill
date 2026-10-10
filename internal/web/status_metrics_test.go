package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
)

func TestMetricsEndpointTokenGate(t *testing.T) {
	metrics.Reset()
	t.Cleanup(metrics.Reset)
	s, q := newTestApp(t)
	h := s.Handler()
	metrics.Inc("login_failures_total", "scope", "admin")

	wantCode(t, do(h, "GET", "/metrics", nil, nil), http.StatusNotFound, "no token configured")

	if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "metrics_token", Value: "tok-123"}); err != nil {
		t.Fatal(err)
	}
	w := do(h, "GET", "/metrics", nil, nil)
	wantCode(t, w, http.StatusUnauthorized, "no bearer header")
	w = do(h, "GET", "/metrics", nil, nil, "Authorization", "Bearer nope")
	wantCode(t, w, http.StatusUnauthorized, "wrong token")

	w = do(h, "GET", "/metrics", nil, nil, "Authorization", "Bearer tok-123")
	wantCode(t, w, http.StatusOK, "right token")
	body := w.Body.String()
	for _, want := range []string{
		"# HELP gobill_login_failures_total ", "# TYPE gobill_login_failures_total counter",
		`gobill_login_failures_total{scope="admin"} 1`,
		"# TYPE gobill_go_goroutines gauge", "# TYPE gobill_build_info gauge",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/metrics lacks %q:\n%s", want, body)
		}
	}
}

func TestStatusPageAndJSONRoles(t *testing.T) {
	s, q := newTestApp(t)
	s.lang.Store("english")
	h := s.Handler()
	if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "alert_nas_silent_minutes", Value: "15"}); err != nil {
		t.Fatal(err)
	}
	alice, rita := login(t, h, "alice"), login(t, h, "rita")

	w := do(h, "GET", "/admin/status", nil, alice)
	wantCode(t, w, http.StatusOK, "status page for SuperAdmin")
	if !strings.Contains(w.Body.String(), "Storage") || !strings.Contains(w.Body.String(), "Background jobs") {
		t.Fatalf("status page lacks sections: %.300s", w.Body.String())
	}
	wantCode(t, do(h, "GET", "/admin/status", nil, rita), http.StatusForbidden, "status page for Report")

	w = do(h, "GET", "/admin/status.json", nil, alice)
	wantCode(t, w, http.StatusOK, "status json for SuperAdmin")
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "uptime", "db_mb", "disk_free_mb", "radius_accepted_24h", "jobs", "notifications", "security", "backup", "metrics_enabled"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("status.json lacks %q: %v", k, out)
		}
	}
	wantCode(t, do(h, "GET", "/admin/status.json", nil, rita), http.StatusForbidden, "status json for Report")
}

func TestMetricsTokenActions(t *testing.T) {
	s, q := newTestApp(t)
	s.lang.Store("english")
	h := s.Handler()
	alice := login(t, h, "alice")
	ctx := t.Context()

	wantCode(t, do(h, "POST", "/admin/settings/integrations/metrics-token", url.Values{}, alice), http.StatusSeeOther, "generate")
	st, err := q.ListSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var tok string
	for _, r := range st {
		if r.Key == "metrics_token" {
			tok = r.Value
		}
	}
	if len(tok) != 64 {
		t.Fatalf("token %q, want 64 hex chars", tok)
	}
	wantCode(t, do(h, "GET", "/metrics", nil, nil, "Authorization", "Bearer "+tok), http.StatusOK, "new token works")

	// shown once: the next settings page does not repeat it
	first := do(h, "GET", "/admin/settings/integrations", nil, alice).Body.String()
	if !strings.Contains(first, tok) {
		t.Fatal("token not shown right after generation")
	}
	if strings.Contains(do(h, "GET", "/admin/settings/integrations", nil, alice).Body.String(), tok) {
		t.Fatal("token shown twice")
	}

	wantCode(t, do(h, "POST", "/admin/settings/integrations/metrics-token/disable", url.Values{}, alice), http.StatusSeeOther, "disable")
	wantCode(t, do(h, "GET", "/metrics", nil, nil, "Authorization", "Bearer "+tok), http.StatusNotFound, "disabled")
}
