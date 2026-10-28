package web

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Admin profile dropdown links to the 2FA page, for every role that sees the dropdown.
func TestProfileDropdownLinks2FA(t *testing.T) {
	_, h, _, c := crudApp(t)
	if body := do(h, "GET", "/admin", nil, c).Body.String(); !strings.Contains(body, `href="/admin/2fa"`) || !strings.Contains(body, "Verifikasi dua langkah (2FA)") {
		t.Fatal("2FA link missing from the profile dropdown")
	}
}

// Expiring-soon rows show the time left ("... lagi"), in warning colour when the plan ends today.
func TestDashboardExpiringTodayBadge(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "p1", "PPPoE", 25000)
	now := time.Now().In(e.srv.location())
	// last second of today in the app time zone: "ends today" at any wall-clock hour
	endOfToday := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).Unix() - 1
	if _, err := e.q.CreateSubscription(t.Context(), db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: p.ID,
		RouterID: sql.NullInt64{Int64: e.rt, Valid: true}, Type: "PPPoE", StartedAt: now.Unix(), ExpiresAt: endOfToday, Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	const badge = `font-medium text-warn-fg"`
	if body := do(e.h, "GET", "/admin", nil, e.c).Body.String(); !strings.Contains(body, badge) || !strings.Contains(body, " lagi</td>") {
		t.Fatal("no time left in warning colour for a plan ending today")
	}
	if _, err := e.srv.conn.Exec(`UPDATE subscriptions SET expires_at = ?`, now.AddDate(0, 0, 3).Unix()); err != nil {
		t.Fatal(err)
	}
	if body := do(e.h, "GET", "/admin", nil, e.c).Body.String(); strings.Contains(body, badge) {
		t.Fatal("warning colour on a plan ending in 3 days")
	}
}

// The disabled "Pay with Balance" button explains itself and links to the top-up path or the operator.
func TestPortalDisabledBalanceLink(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 1000)
	e.plan(t, "Gold", "PPPoE", 10000)
	setting(t, e, "enable_balance", "yes", "phone", "081234567890", "country_code_phone", "62")
	c, _ := custLogin(t, e, "u1", "pw12345")
	body := do(e.h, "GET", "/portal/plans", nil, c).Body.String()
	if !strings.Contains(body, "https://wa.me/6281234567890") || !strings.Contains(body, "Minta admin mengisi saldo") {
		t.Fatal("no operator link under the disabled balance button")
	}
}

// The WhatsApp/QRIS/Tripay/Isolir items are for SuperAdmin only; the registration username is gone.
func TestSettingsRegistrationUsernameRemoved(t *testing.T) {
	e := billApp(t)
	if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "registration_username", Value: "phone"}); err != nil {
		t.Fatal(err)
	}
	if body := do(e.h, "GET", "/admin/settings/billing", nil, e.c).Body.String(); strings.Contains(body, "registration_username") {
		t.Fatal("registration_username still on the settings page")
	}
	if w := do(e.h, "POST", "/admin/settings/billing", url.Values{"voucher_format": {"numbers"}}, e.c); w.Code != 303 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if got := settingValues(t, e.q)["registration_username"]; got != "phone" {
		t.Fatalf("stored value changed to %q", got)
	}
}
