package billing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
)

// withNotify points the service at a fake server; got receives "path?query body" per request.
func withNotify(t *testing.T, e *env) chan string {
	t.Helper()
	got := make(chan string, 20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.URL.Path + "?" + r.URL.RawQuery + " " + string(b)
	}))
	t.Cleanup(srv.Close)
	set := map[string]string{"webhook_url": srv.URL + "/hook", "telegram_bot": "T", "user_notification_expired": "wa",
		"user_notification_reminder": "wa", "wa_url": srv.URL + "/wa?n=[number]&t=[text]"}
	for k, v := range set {
		if err := e.q.UpsertSetting(context.Background(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := notify.Load(context.Background(), e.q)
	if err != nil {
		t.Fatal(err)
	}
	n.TelegramAPI = srv.URL
	e.s.Reload(n, jkt)
	return got
}

// drain collects requests until none arrives for 300ms.
func drain(got chan string) (out []string) {
	for {
		select {
		case s := <-got:
			out = append(out, s)
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

func count(reqs []string, sub string) (n int) {
	for _, r := range reqs {
		if strings.Contains(r, sub) {
			n++
		}
	}
	return
}

func TestRechargeNotifies(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	got := withNotify(t, e)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	reqs := drain(got)
	if count(reqs, "payment.paid") != 1 || count(reqs, "customer.activated") != 1 || len(reqs) != 2 {
		t.Fatalf("first recharge: %v", reqs)
	}
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	if reqs = drain(got); count(reqs, "payment.paid") != 1 || count(reqs, "customer.activated") != 0 {
		t.Fatalf("extend: %v", reqs)
	}
}

func TestExpiryNotifies(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.conn.Exec(`UPDATE customers SET phone = "08123456789"`)
	got := withNotify(t, e)
	e.now = e.now.Add(48 * time.Hour)
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	reqs := drain(got)
	if count(reqs, "recharge.expired") != 1 || count(reqs, "expired") < 2 { // webhook + wa message
		t.Fatalf("expiry: %v", reqs)
	}
}

func TestAutoRenewFailureTelegram(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0)
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 50000, ID: e.cust.ID})
	if _, err := e.conn.Exec(`CREATE TRIGGER boom BEFORE INSERT ON transactions BEGIN SELECT RAISE(ABORT,'boom'); END`); err != nil {
		t.Fatal(err)
	}
	got := withNotify(t, e)
	e.now = e.now.Add(48 * time.Hour)
	e.s.ExpireDue(ctx)
	if reqs := drain(got); count(reqs, "/botT/sendMessage") != 1 || count(reqs, "FAILED+RENEWAL") != 1 {
		t.Fatalf("telegram: %v", reqs)
	}
}

func TestReminderPicksH137Once(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	got := withNotify(t, e)
	base := time.Date(2025, 1, 10, 7, 0, 0, 0, jkt)
	for i, d := range []int{1, 2, 3, 7, 8} { // distinct customers; only 1, 3, 7 qualify
		c, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "r" + string(rune('a'+i)), PasswordHash: "h", Fullname: "R", Phone: "08123456789", ServiceType: "PPPoE", Status: "Active"})
		if err != nil {
			t.Fatal(err)
		}
		exp := time.Date(2025, 1, 10+d, 23, 0, 0, 0, jkt).Unix()
		if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: e.day.ID, RouterID: e.day.RouterID,
			Type: "PPPoE", StartedAt: base.Unix(), ExpiresAt: exp, Method: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	job := e.s.ReminderJob(func() bool { return true })
	e.now = base.Add(-time.Hour) // wrong hour: nothing
	job(ctx)
	e.now = base
	if err := job(ctx); err != nil {
		t.Fatal(err)
	}
	job(ctx) // same day: no resend
	if reqs := drain(got); count(reqs, "/wa") != 3 {
		t.Fatalf("reminders: %v", reqs)
	}
	e.now = base.Add(24 * time.Hour) // next day sends again
	job(ctx)
	if reqs := drain(got); len(reqs) == 0 {
		t.Fatal("no reminder next day")
	}
}

func TestNilNotifierNoop(t *testing.T) {
	ctx, e := context.Background(), setup(t) // N is nil
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(48 * time.Hour)
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.s.ReminderJob(func() bool { return true })(ctx); err != nil {
		t.Fatal(err)
	}
}
