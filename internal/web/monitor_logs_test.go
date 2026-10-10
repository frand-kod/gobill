package web

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestRadiusLogFilterAndCSV(t *testing.T) {
	_, h, q, c := crudApp(t)
	ctx := t.Context()
	day := func(d string) int64 {
		tm, _ := time.Parse("2006-01-02 15:04", d+" 12:00")
		return tm.Unix()
	}
	add := func(user, nas, start string, stopped bool) {
		p := db.UpsertRadiusSessionParams{SessionID: user + start, Username: user, NasIp: nas, StartedAt: day(start), UpdatedAt: day(start), InputOctets: 5, OutputOctets: 7}
		if stopped {
			p.StoppedAt = sql.NullInt64{Int64: day(start) + 60, Valid: true}
		}
		if err := q.UpsertRadiusSession(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	add("budi", "10.0.0.1", "2025-01-10", true)
	add("budi", "10.0.0.1", "2025-02-10", false)
	add("=evil", "10.0.0.2", "2025-02-11", true)

	body := func(u string) string { return do(h, "GET", u, nil, c).Body.String() }
	all := body("/admin/logs/radius")
	if strings.Count(all, "<tr>") != 4 { // header + 3 rows, open and closed
		t.Fatalf("want 3 rows: %s", all)
	}
	if b := body("/admin/logs/radius?q=10.0.0.2"); strings.Contains(b, "budi") || !strings.Contains(b, "=evil") {
		t.Fatal("NAS search")
	}
	if b := body("/admin/logs/radius?from=2025-02-01&to=2025-02-10"); !strings.Contains(b, "2025-02-10") || strings.Contains(b, "2025-01-10") || strings.Contains(b, "=evil") {
		t.Fatalf("date range (to is inclusive): %s", b)
	}
	w := do(h, "GET", "/admin/logs/radius/export?from=2025-02-01", nil, c)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatal(ct)
	}
	csv := w.Body.String()
	if !strings.Contains(csv, "'=evil") || strings.Contains(csv, ",=evil") || strings.Contains(csv, "2025-01-10") || strings.Count(csv, "\n") != 3 {
		t.Fatalf("csv: %q", csv)
	}
}

func TestMessageLogPage(t *testing.T) {
	_, h, q, c := crudApp(t)
	if err := q.CreateMessageLog(t.Context(), db.CreateMessageLogParams{Channel: "sms", Recipient: "0812", Body: "hi", Status: "error", Error: "HTTP 500"}); err != nil {
		t.Fatal(err)
	}
	if b := do(h, "GET", "/admin/logs/messages?q=0812", nil, c).Body.String(); !strings.Contains(b, "HTTP 500") {
		t.Fatalf("log missing: %s", b)
	}
	if b := do(h, "GET", "/admin/logs/messages?q=nobody", nil, c).Body.String(); strings.Contains(b, "HTTP 500") {
		t.Fatal("search ignored")
	}
	if csv := do(h, "GET", "/admin/logs/messages/export", nil, c).Body.String(); !strings.Contains(csv, "0812") {
		t.Fatal(csv)
	}
}

func TestRechargeConfirmPreviewWritesNothing(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	p := e.plan(t, "day", "PPPoE", 10000)
	base := "/admin/customers/" + itoa(e.cust.ID) + "/recharge"
	form := url.Values{"plan": {itoa(p.ID)}, "method": {"Cash"}}
	counts := func() (subs, trx int) {
		s, _ := e.q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
		tr, _ := e.q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true}, Limit: 10})
		return len(s), len(tr)
	}
	w := do(e.h, "POST", base+"/confirm", form, e.c)
	wantCode(t, w, 200, "confirm")
	if b := w.Body.String(); !strings.Contains(b, "Rp 10.000") || !strings.Contains(b, `action="`+base+`"`) || !strings.Contains(b, "new-expiry") {
		t.Fatalf("summary: %s", b)
	}
	if s, tr := counts(); s != 0 || tr != 0 {
		t.Fatalf("preview wrote data: %d subs, %d trx", s, tr)
	}

	// an active sub of the same plan is extended: the preview must show old expiry + 1 day
	wantCode(t, do(e.h, "POST", base, form, e.c), 303, "recharge")
	subs, _ := e.q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	want := time.Unix(subs[0].ExpiresAt, 0).UTC().AddDate(0, 0, 1).Format("2006-01-02 15:04")
	if b := do(e.h, "POST", base+"/confirm", form, e.c).Body.String(); !strings.Contains(b, want) {
		t.Fatalf("want expiry %s in: %s", want, b)
	}
	after, _ := e.q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	if len(after) != 1 || after[0].ExpiresAt != subs[0].ExpiresAt {
		t.Fatalf("preview changed the subscription: %+v", after)
	}

	// balance method shows the balance after and blocks when short
	b := do(e.h, "POST", base+"/confirm", url.Values{"plan": {itoa(p.ID)}, "method": {"Balance"}}, e.c).Body.String()
	if !strings.Contains(b, `id="balance-after">Rp -10.000`) || !strings.Contains(b, "disabled") {
		t.Fatalf("balance preview: %s", b)
	}
	wantCode(t, do(e.h, "POST", base+"/confirm", url.Values{"plan": {"999"}, "method": {"Cash"}}, e.c), 303, "bad plan")
}

func TestVoucherViewAfterGenerate(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	w := do(e.h, "POST", "/admin/vouchers", url.Values{"plan": {itoa(p.ID)}, "numbervoucher": {"2"}, "voucher_format": {"up"}, "lengthcode": {"8"}}, e.c)
	wantCode(t, w, 303, "generate")
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/vouchers/view?") {
		t.Fatal(loc)
	}
	vs, _ := e.q.SearchVouchers(t.Context(), db.SearchVouchersParams{PageLimit: 10})
	b := do(e.h, "GET", loc, nil, e.c).Body.String()
	if len(vs) != 2 || !strings.Contains(b, vs[0].Code) || !strings.Contains(b, vs[1].Code) {
		t.Fatalf("codes missing: %s", b)
	}
}

func TestRouterStatusInListAndDashboard(t *testing.T) {
	e := billApp(t)
	if b := do(e.h, "GET", "/admin/routers", nil, e.c).Body.String(); !strings.Contains(b, "Tidak diketahui") {
		t.Fatal("unchecked router should be Unknown")
	}
	if err := e.q.SetRouterStatus(t.Context(), db.SetRouterStatusParams{ID: e.rt, Online: sql.NullInt64{Int64: 0, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if b := do(e.h, "GET", "/admin/routers", nil, e.c).Body.String(); !strings.Contains(b, "badge-bad") {
		t.Fatal("offline badge missing")
	}
	// no banner: the Network card lists the offline router instead
	if b := do(e.h, "GET", "/admin", nil, e.c).Body.String(); strings.Contains(b, "alert-warn") || strings.Contains(b, "Router offline") {
		t.Fatal("dashboard should not show a router-offline banner")
	}
}

func TestSingleSessionKillsOldSession(t *testing.T) {
	s, q := newTestApp(t)
	h := s.Handler()
	first := login(t, h, "alice")
	second := login(t, h, "alice")
	wantCode(t, do(h, "GET", "/admin", nil, first), 200, "default allows many sessions")

	if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "single_session", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	s.ReloadSessionSettings(t.Context())
	third := login(t, h, "alice")
	wantCode(t, do(h, "GET", "/admin", nil, first), 303, "first session")
	wantCode(t, do(h, "GET", "/admin", nil, second), 303, "second session")
	wantCode(t, do(h, "GET", "/admin", nil, third), 200, "new session")
}

func TestIdleTimeoutApplied(t *testing.T) {
	s, q := newTestApp(t)
	h := s.Handler()
	if got := time.Duration(s.idle.Load()); got != defaultIdle {
		t.Fatalf("default idle %v", got)
	}
	if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "session_timeout_duration", Value: "45"}); err != nil {
		t.Fatal(err)
	}
	s.ReloadSessionSettings(t.Context())
	if got := time.Duration(s.idle.Load()); got != 45*time.Minute {
		t.Fatalf("idle %v, want 45m", got)
	}
	c := login(t, h, "alice")
	wantCode(t, do(h, "GET", "/admin", nil, c), 200, "active session")
	s.idle.Store(int64(time.Second)) // shrink the limit instead of waiting minutes
	time.Sleep(2200 * time.Millisecond)
	w := do(h, "GET", "/admin", nil, c)
	if w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatalf("idle session still valid: %d", w.Code)
	}
}
