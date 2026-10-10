package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// englishApp is billApp with English pages (the test app defaults to Indonesian).
func englishApp(t *testing.T) *billEnv {
	e := billApp(t)
	e.s.lang.Store("english")
	return e
}

// dashPlan gives u1 an active RADIUS plan that ends at expires. limitType "" = unlimited.
func dashPlan(t *testing.T, e *billEnv, limitType string, timeHrs, dataMB, expires int64) {
	t.Helper()
	p := db.CreatePlanParams{Name: "Gold", Type: "PPPoE", Billing: "prepaid", Price: 1, Validity: 30, ValidityUnit: "Days", Enabled: 1, Device: "Radius",
		BandwidthID: sql.NullInt64{Int64: e.bw, Valid: true}}
	if limitType != "" {
		p.Limited, p.LimitType = 1, sql.NullString{String: limitType, Valid: true}
		if timeHrs > 0 {
			p.TimeLimit, p.TimeUnit = sql.NullInt64{Int64: timeHrs, Valid: true}, sql.NullString{String: "Hrs", Valid: true}
		}
		if dataMB > 0 {
			p.DataLimit, p.DataUnit = sql.NullInt64{Int64: dataMB, Valid: true}, sql.NullString{String: "MB", Valid: true}
		}
	}
	pl, err := e.q.CreatePlan(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateSubscription(t.Context(), db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: pl.ID, Type: "PPPoE",
		StartedAt: time.Now().Unix() - 20*3600, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
}

// dashSess records a RADIUS session. stop 0 = still open (updated now).
func dashSess(t *testing.T, e *billEnv, user, sid, ip, mac string, start, stop, in, out int64) {
	t.Helper()
	upd := stop
	if stop == 0 {
		upd = time.Now().Unix()
	}
	if err := e.q.UpsertRadiusSession(t.Context(), db.UpsertRadiusSessionParams{SessionID: sid, Username: user, NasIp: "192.0.2.1", FramedIp: ip, Mac: mac,
		StartedAt: start, UpdatedAt: upd, StoppedAt: sql.NullInt64{Int64: stop, Valid: stop != 0}, InputOctets: in, OutputOctets: out}); err != nil {
		t.Fatal(err)
	}
}

// otherCust is a second customer whose data must never show up for u1.
func otherCust(t *testing.T, e *billEnv) db.Customer {
	t.Helper()
	c, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "U Two", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func portalGet(t *testing.T, e *billEnv, c *http.Cookie, target string) string {
	t.Helper()
	w := do(e.h, "GET", target, nil, c)
	if w.Code != 200 {
		t.Fatalf("GET %s: %d", target, w.Code)
	}
	return w.Body.String()
}

// The default language (Indonesian) is what customers see: the card states in Indonesian.
func TestPortalPlanCardStates(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	body := portalGet(t, e, c, "/portal")
	if !strings.Contains(body, "Anda belum punya paket aktif.") || !strings.Contains(body, `href="/portal/plans"`) {
		t.Fatal("no plan: message and buy button missing")
	}
	now := time.Now().Unix()
	dashPlan(t, e, "", 0, 0, now+3*86400+4*3600+120)
	body = portalGet(t, e, c, "/portal")
	if !strings.Contains(body, "Gold") || !strings.Contains(body, "3 hari 4 jam lagi") {
		t.Fatal("active plan: name or countdown missing")
	}
	if !strings.Contains(body, "x-text=\"left\"") {
		t.Fatal("countdown ticker missing")
	}
	if !strings.Contains(body, "Tanpa batas") {
		t.Fatal("unlimited plan should say Tanpa batas")
	}
}

// An ended plan with extend_expired on shows the renew button; off, it does not.
func TestPortalEndedPlanExtend(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	dashPlan(t, e, "", 0, 0, time.Now().Unix()-60)
	body := portalGet(t, e, c, "/portal")
	if !strings.Contains(body, "Plan Gold ended on") || strings.Contains(body, "/portal/extend/") {
		t.Fatal("ended plan without extend_expired: no renew button expected")
	}
	if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "extend_expired", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	// the expiry sweep marks the subscription expired; the renew button is for those
	if _, err := e.s.conn.ExecContext(t.Context(), `UPDATE subscriptions SET status = 'expired'`); err != nil {
		t.Fatal(err)
	}
	if body := portalGet(t, e, c, "/portal"); !strings.Contains(body, `action="/portal/extend/`) {
		t.Fatal("extend_expired on: renew button missing")
	}
}

func TestPortalUsageMetersAndIsolation(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	now := time.Now().Unix()
	dashPlan(t, e, "Both_Limit", 10, 1000, now+86400)
	// 8 h and 800 MB used: 80% is not above the warn line
	dashSess(t, e, "u1", "a", "10.1.0.1", "CE:33:AA:BB:2A:AA", now-19*3600, now-11*3600, 400<<20, 400<<20)
	// another customer's usage must not count
	other := otherCust(t, e)
	if err := e.q.UpsertRadiusSession(t.Context(), db.UpsertRadiusSessionParams{SessionID: "o", Username: other.Username, NasIp: "192.0.2.1", FramedIp: "10.9.9.9",
		Mac: "DE:AD:BE:EF:00:01", StartedAt: now - 5*3600, UpdatedAt: now, InputOctets: 500 << 30}); err != nil {
		t.Fatal(err)
	}
	body := portalGet(t, e, c, "/portal")
	if strings.Contains(body, "bg-warn-fg") || !strings.Contains(body, `aria-valuenow="80"`) {
		t.Fatal("80% should be a plain bar")
	}
	if strings.Contains(body, "10.9.9.9") {
		t.Fatal("other customer's session shown")
	}
	// 1 h more and 50 MB more: 90% time, 85% data, both warn
	dashSess(t, e, "u1", "b", "10.1.0.1", "CE:33:AA:BB:2A:AA", now-10*3600, now-9*3600, 25<<20, 25<<20)
	body = portalGet(t, e, c, "/portal")
	if strings.Count(body, "bg-warn-fg") != 2 || !strings.Contains(body, `aria-valuenow="90"`) || !strings.Contains(body, `aria-valuenow="85"`) {
		t.Fatalf("over 80%% should warn on both meters")
	}
	if strings.Contains(body, "10.9.9.9") || strings.Contains(body, "DE:AD:BE:EF") {
		t.Fatal("other customer's usage leaked")
	}
}

func TestPortalUnlimitedAndOverLimit(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	now := time.Now().Unix()
	dashPlan(t, e, "Data_Limit", 0, 100, now+86400)
	dashSess(t, e, "u1", "a", "10.1.0.1", "", now-3600, now-60, 200<<20, 0) // 200% of 100 MB
	body := portalGet(t, e, c, "/portal")
	if !strings.Contains(body, `aria-valuenow="100"`) || strings.Contains(body, "Online time") {
		t.Fatal("over limit: bar must cap at 100 and no time meter without a time limit")
	}
}

func TestPortalConnectionOnlineOffline(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	now := time.Now().Unix()
	dashSess(t, e, "u1", "open", "10.0.0.5", "CE:33:AA:BB:2A:AA", now-600, 0, 1, 2)
	body := portalGet(t, e, c, "/portal")
	for _, want := range []string{"10.0.0.5", "CE:33:••:••:2A:AA", "badge-ok"} {
		if !strings.Contains(body, want) {
			t.Fatalf("online card lacks %q", want)
		}
	}
	if strings.Contains(body, "CE:33:AA:BB:2A:AA") {
		t.Fatal("full MAC shown to the customer")
	}
	// the session went silent: offline, last seen shown
	if _, err := e.s.conn.ExecContext(t.Context(), `UPDATE radius_sessions SET updated_at = ? WHERE session_id = 'open'`, now-3600); err != nil {
		t.Fatal(err)
	}
	body = portalGet(t, e, c, "/portal")
	if !strings.Contains(body, "Last seen") || strings.Contains(body, "Connected since") {
		t.Fatal("silent session should read as offline with last seen")
	}
}

func TestPortalSessionHistoryPagingAndIsolation(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	now := time.Now().Unix()
	for i := 1; i <= 12; i++ {
		dashSess(t, e, "u1", fmt.Sprintf("s%d", i), fmt.Sprintf("10.1.0.%d", i), fmt.Sprintf("CE:33:00:00:00:%02X", i), now-int64(i)*7200, now-int64(i)*7200+600, 1, 1)
	}
	other := otherCust(t, e)
	dashSess(t, e, other.Username, "x", "10.9.9.9", "DE:AD:BE:EF:00:01", now-600, now, 1, 1)

	page1 := portalGet(t, e, c, "/portal")
	if n := strings.Count(page1, `<td class="font-mono">`); n != 10 {
		t.Fatalf("page 1 rows = %d, want 10", n)
	}
	if !strings.Contains(page1, "/portal?spage=2#riwayat") || strings.Contains(page1, "/portal?spage=1#riwayat") {
		t.Fatal("page 1 pager wrong")
	}
	page2 := portalGet(t, e, c, "/portal?spage=2")
	if n := strings.Count(page2, `<td class="font-mono">`); n != 2 {
		t.Fatalf("page 2 rows = %d, want 2", n)
	}
	if !strings.Contains(page2, "/portal?spage=1#riwayat") || strings.Contains(page2, "/portal?spage=3#riwayat") {
		t.Fatal("page 2 pager wrong")
	}
	for _, p := range []string{page1, page2} {
		if strings.Contains(p, "10.9.9.9") || strings.Contains(p, "DE:AD:BE:EF") {
			t.Fatal("another customer's session in the history")
		}
	}
	// garbage page numbers fall back to page 1
	if n := strings.Count(portalGet(t, e, c, "/portal?spage=abc"), `<td class="font-mono">`); n != 10 {
		t.Fatalf("bad spage rows = %d", n)
	}
}

func TestPortalActivationPagingAndIsolation(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	c, _ := custLogin(t, e, "u1", "pw12345")
	other := otherCust(t, e)
	ctx := t.Context()
	mk := func(cust int64, inv, typ string) {
		if _, err := e.q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: inv, CustomerID: sql.NullInt64{Int64: cust, Valid: true},
			Username: "x", PlanName: "P", Type: typ, Price: 1, Method: "Cash", PeriodStart: 1, PeriodEnd: 2}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 25; i++ {
		mk(e.cust.ID, fmt.Sprintf("INV-%02d", i), "PPPoE")
	}
	mk(e.cust.ID, "BAL-1", "Balance")
	mk(other.ID, "OTHER-1", "PPPoE")

	p1 := portalGet(t, e, c, "/portal/activation")
	if n := strings.Count(p1, "/invoice\">"); n != 20 {
		t.Fatalf("page 1 rows = %d, want 20", n)
	}
	if !strings.Contains(p1, "INV-25") || strings.Contains(p1, "INV-05") || !strings.Contains(p1, "/portal/activation?page=2") {
		t.Fatal("page 1 content wrong")
	}
	p2 := portalGet(t, e, c, "/portal/activation?page=2")
	if n := strings.Count(p2, "/invoice\">"); n != 5 || !strings.Contains(p2, "INV-01") {
		t.Fatalf("page 2 rows = %d", n)
	}
	for _, p := range []string{p1, p2} {
		if strings.Contains(p, "BAL-1") || strings.Contains(p, "OTHER-1") {
			t.Fatal("balance move or other customer's invoice listed as activation")
		}
	}
}

func TestPortalLastLoginRecorded(t *testing.T) {
	e := englishApp(t)
	portalCust(t, e, 0)
	if cu, _ := e.q.GetCustomer(t.Context(), e.cust.ID); cu.LastLoginAt.Valid {
		t.Fatal("last login set before any login")
	}
	before := time.Now().Unix()
	c, code := custLogin(t, e, "u1", "pw12345")
	if code != http.StatusSeeOther || c == nil {
		t.Fatalf("login: %d", code)
	}
	cu, _ := e.q.GetCustomer(t.Context(), e.cust.ID)
	if !cu.LastLoginAt.Valid || cu.LastLoginAt.Int64 < before {
		t.Fatalf("last login not recorded: %+v", cu.LastLoginAt)
	}
	if body := portalGet(t, e, c, "/portal"); !strings.Contains(body, `href="/portal/profile#password"`) {
		t.Fatal("security card lacks the change password link")
	}
}

func TestMaskMAC(t *testing.T) {
	for in, want := range map[string]string{
		"CE:33:AA:BB:2A:AA": "CE:33:••:••:2A:AA",
		"ce-33-aa-bb-2a-aa": "ce:33:••:••:2a:aa",
		"":                  "",
		"garbage":           "",
	} {
		if got := maskMAC(in); got != want {
			t.Errorf("maskMAC(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPwordsCountdownText(t *testing.T) {
	w := pWords{Days: "hari", Hours: "jam", Minutes: "menit", Left: "lagi", Done: "Sudah habis"}
	for sec, want := range map[int64]string{
		3*86400 + 4*3600 + 59: "3 hari 4 jam lagi",
		86400:                 "1 hari lagi",
		2*3600 + 15*60:        "2 jam 15 menit lagi",
		2 * 3600:              "2 jam lagi",
		45 * 60:               "45 menit lagi",
		30:                    "1 menit lagi",
		0:                     "Sudah habis",
		-5:                    "Sudah habis",
	} {
		if got := w.countdown(sec); got != want {
			t.Errorf("countdown(%d) = %q, want %q", sec, got, want)
		}
	}
}
