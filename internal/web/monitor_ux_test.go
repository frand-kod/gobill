package web

import (
	"database/sql"
	"fmt"
	"github.com/frand-kod/gobill/internal/web/view"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/i18n"
)

// Every label the monitoring pages map to a key must be translated in lang/indonesia.json.
func TestMonitorLabelMapsTranslated(t *testing.T) {
	cat, err := i18n.Load(gobill.FS, "lang")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range actionLabels {
		if cat["indonesia"][v] == "" {
			t.Errorf("action %q: label %q not translated", k, v)
		}
	}
	for k, v := range rejectLabels {
		if cat["indonesia"][v] == "" {
			t.Errorf("reject %q: label %q not translated", k, v)
		}
	}
}

// The activity log names the action and the admin who did it; an unknown action keeps its key.
func TestActivityLogLabelsAndActor(t *testing.T) {
	e := englishApp(t)
	ctx := t.Context()
	admin, err := e.q.GetAdminByUsername(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.s.actionLabel("logs.clean"); got != "Logs cleaned" {
		t.Errorf("logs.clean label = %q", got)
	}
	if got := e.s.actionLabel("made.up.action"); got != "made.up.action" {
		t.Errorf("unknown action label = %q, want the key", got)
	}
	if got := e.s.actorLabel(ctx, "admin", 9999); got != "admin #9999" {
		t.Errorf("missing admin actor = %q", got)
	}
	if err := e.q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "admin", ActorID: admin.ID, Action: "logs.clean", Description: "actor-marker"}); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"/admin/logs", "/admin"} {
		body := do(e.h, "GET", page, nil, e.c).Body.String()
		for _, want := range []string{"Logs cleaned", "alice", "actor-marker"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", page, want)
			}
		}
		if strings.Contains(body, "admin #") {
			t.Errorf("%s: still shows admin #N", page)
		}
	}
}

// The clean flash names the number of deleted rows, not a garbled "older than" phrase.
func TestLogCleanFlashCount(t *testing.T) {
	e := englishApp(t)
	if w := do(e.h, "POST", "/admin/logs/clean/activity", url.Values{"keep": {"30"}}, e.c); w.Code != http.StatusSeeOther {
		t.Fatalf("clean: status %d", w.Code)
	}
	if body := do(e.h, "GET", "/admin/logs", nil, e.c).Body.String(); !strings.Contains(body, "0 logs deleted") {
		t.Errorf("flash after clean does not name the count")
	}
	if got := fmt.Sprintf(e.s.catalog.T("indonesia", "%d logs deleted"), 3); got != "3 log dihapus" {
		t.Errorf("indonesian flash = %q", got)
	}
}

// The activity log page links to the login attempts log next to the radius and message logs.
func TestLogsPageLinksLoginAttempts(t *testing.T) {
	e := englishApp(t)
	body := do(e.h, "GET", "/admin/logs", nil, e.c).Body.String()
	if !strings.Contains(body, `href="/admin/logs/auth"`) {
		t.Error("logs page has no link to login attempts")
	}
}

// The income tile names the start of its period when reset_day moves it.
func TestDashboardIncomeLabelResetDay(t *testing.T) {
	e := englishApp(t)
	ctx := t.Context()
	if body := do(e.h, "GET", "/admin", nil, e.c).Body.String(); !strings.Contains(body, "Income This Month") {
		t.Error("default month label missing")
	}
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "reset_day", Value: "15"}); err != nil {
		t.Fatal(err)
	}
	if body := do(e.h, "GET", "/admin", nil, e.c).Body.String(); !strings.Contains(body, "Income this period (since day 15)") {
		t.Error("reset_day label missing")
	}
}

// The sales chart reads the currency prefix and separator from view.Money() through data attributes.
func TestDashboardChartMoneyAttributes(t *testing.T) {
	e := englishApp(t)
	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	for _, want := range []string{`data-money-prefix="Rp "`, fmt.Sprintf(`data-money-sep="%s"`, view.GroupSep())} {
		if !strings.Contains(body, want) {
			t.Errorf("sales chart missing %s", want)
		}
	}
}

// Dashboard rows: the recharge icon goes to the recharge page for that customer and names the username;
// with 50 expiring rows (the cap) the tab count reads "50+".
func TestDashboardRowsAndCappedCount(t *testing.T) {
	e := englishApp(t)
	ctx := t.Context()
	p := e.plan(t, "cap", "PPPoE", 1000)
	now := time.Now().Unix()
	for i := 0; i < dashListLimit+5; i++ {
		c, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: fmt.Sprintf("cap%02d", i), PasswordHash: "h", Fullname: "Cap", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: p.ID, RouterID: sql.NullInt64{Int64: e.rt, Valid: true},
			Type: "PPPoE", StartedAt: now, ExpiresAt: now + 3600, Method: "Cash"}); err != nil {
			t.Fatal(err)
		}
	}
	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	if !strings.Contains(body, `aria-label="Recharge cap00"`) || !strings.Contains(body, `href="/admin/recharge?customer=cap00"`) {
		t.Error("expiring row: recharge icon does not name the customer or go to the recharge page")
	}
	if !strings.Contains(body, `data-limit="`+strconv.Itoa(dashListLimit)+`"`) {
		t.Error("customers card has no data-limit")
	}
	if !strings.Contains(body, `">50+</span>`) {
		t.Error("capped tab count is not 50+")
	}
}

// The status page states what its counters cover, names the job failures and has a pause toggle instead of a
// blind reload.
func TestStatusPageLabelsAndPause(t *testing.T) {
	e := englishApp(t)
	body := do(e.h, "GET", "/admin/status", nil, e.c).Body.String()
	for _, want := range []string{"Pause refresh", "Jobs failed: 0 of 0", "since app start", `id="st-pause"`, "Days until disk full: Not known"} {
		if !strings.Contains(body, want) {
			t.Errorf("status page missing %q", want)
		}
	}
	if strings.Contains(body, "location.reload(); }, 30000);") {
		t.Error("status page still reloads blindly")
	}
}

// Test login: empty fields are field errors; a reject is an alert above the form, with the reason in words.
func TestTestLoginErrors(t *testing.T) {
	e := englishApp(t)
	body := do(e.h, "POST", "/admin/radius/test-login", url.Values{"username": {""}, "password": {"x"}}, e.c).Body.String()
	if !strings.Contains(body, "Enter a username") || !strings.Contains(body, `role="alert"`) {
		t.Errorf("empty username: no field error alert")
	}
	body = do(e.h, "POST", "/admin/radius/test-login", url.Values{"username": {"u1"}, "password": {"u1"}}, e.c).Body.String()
	if !strings.Contains(body, "Enter a username and a password that differs from it") {
		t.Errorf("password equal to username: no error")
	}
	body = do(e.h, "POST", "/admin/radius/test-login", url.Values{"username": {"nobody-here"}, "password": {"pw"}}, e.c).Body.String()
	if !strings.Contains(body, "Access-Reject") || !strings.Contains(body, `role="alert"`) {
		t.Errorf("reject: no alert")
	}
}

// IP lookup: the sessions come back as a table with the user, MAC, NAS, start and stop columns.
func TestIPLookupResultIsTable(t *testing.T) {
	e := englishApp(t)
	now := time.Now().Unix()
	if err := e.q.UpsertRadiusSession(t.Context(), db.UpsertRadiusSessionParams{SessionID: "tbl", Username: "tbl-user", NasIp: "10.0.0.1",
		FramedIp: "10.9.9.9", StartedAt: now - 600, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	body := do(e.h, "POST", "/admin/radius/ip-lookup", url.Values{"ip": {"10.9.9.9"}, "at": {""}}, e.c).Body.String()
	for _, want := range []string{"<table", "<th", ">MAC<", ">Stop<", "tbl-user", "still online", "Search again"} {
		if !strings.Contains(body, want) {
			t.Errorf("ip lookup result missing %q", want)
		}
	}
}

// Radius sessions: the Disconnect control names the user it disconnects.
func TestRadiusSessionDisconnectLabel(t *testing.T) {
	e := englishApp(t)
	now := time.Now().Unix()
	if err := e.q.UpsertRadiusSession(t.Context(), db.UpsertRadiusSessionParams{SessionID: "dsc", Username: "dsc-user", NasIp: "10.0.0.1",
		FramedIp: "10.8.8.8", StartedAt: now - 60, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	body := do(e.h, "GET", "/admin/radius/sessions", nil, e.c).Body.String()
	if !strings.Contains(body, `aria-label="Disconnect dsc-user"`) {
		t.Error("disconnect control does not name the user")
	}
}
