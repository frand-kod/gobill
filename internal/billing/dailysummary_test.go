package billing

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
)

func setKV(t *testing.T, e *env, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := e.q.UpsertSetting(context.Background(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
}

// reload refreshes the notifier settings from the DB, keeping the fake Telegram server.
func reload(t *testing.T, e *env) {
	t.Helper()
	old := e.s.notifier()
	n, err := notify.Load(context.Background(), e.q)
	if err != nil {
		t.Fatal(err)
	}
	n.TelegramAPI = old.TelegramAPI
	e.s.Reload(n, jkt)
}

// seedSummary: yesterday = 2025-01-10, today = 2025-01-11 (jkt).
func seedSummary(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	yday := time.Date(2025, 1, 10, 15, 0, 0, 0, jkt).Unix()
	for _, m := range []struct {
		method string
		price  int64
	}{{"Admin - Cash", 10000}, {"Customer - Balance", 99999}, {"Balance - Gift from x", 77777}} {
		tr, err := e.q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: m.method, Username: "u", PlanName: "p", Type: "PPPoE", Price: m.price, Method: m.method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.conn.Exec("UPDATE transactions SET created_at=? WHERE id=?", yday, tr.ID); err != nil {
			t.Fatal(err)
		}
	}
	for i, exp := range []struct {
		day, hour int
		status    string
	}{{11, 20, "active"}, {11, 22, "active"}, {10, 9, "expired"}, {12, 9, "active"}} {
		c, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "s" + string(rune('a'+i)), PasswordHash: "h", Fullname: "S", ServiceType: "PPPoE", Status: "Active"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.conn.Exec("UPDATE customers SET created_at=? WHERE id=?", yday, c.ID); err != nil {
			t.Fatal(err)
		}
		sub, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: e.day.ID, RouterID: e.day.RouterID, Type: "PPPoE",
			StartedAt: yday - 5*86400, ExpiresAt: time.Date(2025, 1, exp.day, exp.hour, 0, 0, 0, jkt).Unix(), Method: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.conn.Exec("UPDATE subscriptions SET status=? WHERE id=?", exp.status, sub.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.q.SetRouterStatus(ctx, db.SetRouterStatusParams{ID: e.day.RouterID.Int64, Online: sql.NullInt64{Int64: 0, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	setKV(t, e, map[string]string{"app_url": "https://bill.example.com/"})
}

func TestDailySummaryText(t *testing.T) {
	e := setup(t)
	seedSummary(t, e)
	got, err := e.s.DailySummaryText(context.Background(), time.Date(2025, 1, 11, 7, 0, 0, 0, jkt))
	if err != nil {
		t.Fatal(err)
	}
	want := "Ringkasan harian 2025-01-11\nPemasukan kemarin: Rp 10.000\nPelanggan baru kemarin: 4\n" +
		"Langganan habis hari ini: 2 (sa, sb)\nLangganan habis kemarin: 1\nRouter offline: r\nDashboard: https://bill.example.com/admin"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func summaryEnv(t *testing.T) (*env, chan string) {
	e := setup(t)
	setKV(t, e, map[string]string{"daily_summary_enabled": "yes", "daily_summary_channel": "telegram", "daily_summary_time": "07:30"})
	got := withNotify(t, e) // sets telegram_bot=T
	setKV(t, e, map[string]string{"telegram_target_id": "42"})
	reload(t, e)
	e.now = time.Date(2025, 1, 11, 7, 29, 0, 0, jkt)
	return e, got
}

func TestDailySummaryOncePerDay(t *testing.T) {
	ctx := context.Background()
	e, got := summaryEnv(t)
	job := e.s.DailySummaryJob(func() bool { return true })
	job(ctx) // before 07:30
	if r := drain(got); len(r) != 0 {
		t.Fatalf("early: %v", r)
	}
	e.now = e.now.Add(time.Minute)
	job(ctx)
	job(ctx)
	if r := drain(got); count(r, "/botT/sendMessage") != 1 || !strings.Contains(r[0], "Ringkasan") {
		t.Fatalf("want 1 send: %v", r)
	}
	// restart: a new job closure, same DB, later the same day: no resend
	e.now = e.now.Add(5 * time.Hour)
	e.s.DailySummaryJob(func() bool { return true })(ctx)
	if r := drain(got); len(r) != 0 {
		t.Fatalf("resent after restart: %v", r)
	}
	e.now = e.now.Add(24 * time.Hour)
	job(ctx)
	if r := drain(got); len(r) != 1 {
		t.Fatalf("next day: %v", r)
	}
}

func TestDailySummarySkips(t *testing.T) {
	ctx := context.Background()
	e, got := summaryEnv(t)
	e.now = e.now.Add(time.Hour)
	if e.s.DailySummaryJob(func() bool { return false })(ctx); len(drain(got)) != 0 {
		t.Fatal("sent with untrusted clock")
	}
	if v := setting(ctx, e.q, "daily_summary_last"); v != "" {
		t.Fatalf("marked sent while untrusted: %s", v)
	}
	setKV(t, e, map[string]string{"daily_summary_enabled": "no"})
	if e.s.DailySummaryJob(func() bool { return true })(ctx); len(drain(got)) != 0 {
		t.Fatal("sent while disabled")
	}
	setKV(t, e, map[string]string{"daily_summary_enabled": "yes", "daily_summary_channel": "wa"}) // wa needs a phone
	reload(t, e)
	if e.s.DailySummaryJob(func() bool { return true })(ctx); len(drain(got)) != 0 {
		t.Fatal("sent without wa recipient")
	}
	if _, err := e.s.SendDailySummary(ctx); err != ErrSummaryNotConfigured {
		t.Fatalf("send now: %v", err)
	}
}
