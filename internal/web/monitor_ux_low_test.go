package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// The login attempts page badges the result (list.html styles a "Status" column) and translates the reject reason.
func TestAuthLogBadgeAndReasonLabel(t *testing.T) {
	e := englishApp(t)
	now := time.Now().Unix()
	if err := e.q.InsertRadiusAuthLog(t.Context(), db.InsertRadiusAuthLogParams{At: now, Username: "budi", NasIp: "10.0.0.1", Mac: "AA", Accepted: 1}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.InsertRadiusAuthLog(t.Context(), db.InsertRadiusAuthLogParams{At: now, Username: "ani", NasIp: "10.0.0.1", Mac: "BB", Message: "Voucher Expired..."}); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/admin/logs/auth", nil, e.c)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, want := range []string{`>Status<`, `badge-ok">Accepted<`, `badge-bad">Rejected<`, `Voucher expired`} {
		if !strings.Contains(body, want) {
			t.Errorf("login attempts page missing %q", want)
		}
	}
	if strings.Contains(body, "Voucher Expired...") {
		t.Error("raw reject reason shown instead of its label")
	}
}

// A failing card shows "Data could not be loaded" and logs the error; the rest of the dashboard still renders.
func TestDashboardSurvivesFailingCard(t *testing.T) {
	e := englishApp(t)
	ctx := t.Context()
	if err := e.q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "admin", Action: "Recharge", Description: "card-survives-marker"}); err != nil {
		t.Fatal(err)
	}
	// the usage card reads radius_usage_daily; dropping it makes only that card's query fail
	if _, err := e.s.conn.ExecContext(ctx, "DROP TABLE radius_usage_daily"); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/admin", nil, e.c)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("dashboard status %d, want 200", w.Code)
	}
	if !strings.Contains(body, "Data could not be loaded") {
		t.Error("failing usage card does not show the load error")
	}
	if !strings.Contains(body, "card-survives-marker") {
		t.Error("activity log card missing although its query works")
	}
}

// The Network tile says "offline" and names up to three offline routers.
func TestDashboardTileNamesOfflineRouters(t *testing.T) {
	e := englishApp(t)
	off := sql.NullInt64{Int64: 0, Valid: true}
	for _, n := range []string{"core-a", "core-b", "core-c", "core-d"} {
		mkRouter(t, e.q, n, 1, "api", off, sql.NullInt64{})
	}
	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	i := strings.Index(body, "4 Offline: ")
	if i < 0 {
		t.Fatal("tile line '4 Offline: ...' missing")
	}
	line := body[i+len("4 Offline: "):]
	line = line[:strings.Index(line, "<")]
	names := strings.Split(line, ", ")
	if len(names) != 3 {
		t.Errorf("named %d routers (%q), want 3", len(names), line)
	}
}

// The fallback confirm form (no existing data, so no dialog) asks for the password and the 2FA code too.
func TestImportFallbackFormAsksStepUp(t *testing.T) {
	s, _ := newTestApp(t)
	// the render reads the session and the admin from the request, as the real handler chain sets them
	h := s.Sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, &db.Admin{Role: "SuperAdmin", TotpEnabled: 1}))
		s.renderImport(w, r, http.StatusOK, importView{Token: "0123456789abcdef0123456789abcdef", Filled: false}, "", "")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", importPage, nil))
	body := w.Body.String()
	i := strings.Index(body, `action="/admin/settings/system/import/confirm"`)
	if i < 0 {
		t.Fatal("confirm form missing")
	}
	form := body[i : i+strings.Index(body[i:], "</form>")]
	for _, want := range []string{`name="current"`, `name="code"`, `name="token"`} {
		if !strings.Contains(form, want) {
			t.Errorf("fallback confirm form without %s: it always fails", want)
		}
	}
}
