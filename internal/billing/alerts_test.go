package billing

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/job"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/notify"
)

// alertsFor returns the messages in reqs that mention sub (the telegram stub logs every request).
func alertsFor(reqs []string, sub string) int { return count(reqs, sub) }

func TestNASSilentAlertOncePerEpisode(t *testing.T) {
	metrics.Reset()
	t.Cleanup(metrics.Reset)
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	if _, err := e.q.CreateNAS(ctx, db.CreateNASParams{Name: "pop-a", Ip: "10.9.0.5", SecretEnc: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	job := &AlertJob{S: e.s, Free: func() (int64, error) { return 9999, nil }, Now: func() time.Time { return now }}
	last := now.Add(-time.Minute)
	metrics.Set("radius_nas_last_packet_timestamp", float64(last.Unix()), "nas", "10.9.0.5")
	run := func(what string, want int) {
		t.Helper()
		if err := job.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if n := alertsFor(drain(got), "pop-a"); n != want {
			t.Fatalf("%s: %d NAS alerts, want %d", what, n, want)
		}
	}
	run("fresh packets", 0)
	now = now.Add(20 * time.Minute) // 21 minutes since the last packet
	run("silent past 15 minutes", 1)
	run("still silent", 0)
	metrics.Set("radius_nas_last_packet_timestamp", float64(now.Unix()), "nas", "10.9.0.5")
	run("packets back", 1)
	run("still fine", 0)
}

// A NAS that is not in the nas table (FreeRADIUS in front of /radius.php) still alerts once and recovers once.
func TestNASSilentWithoutTableEntry(t *testing.T) {
	metrics.Reset()
	t.Cleanup(metrics.Reset)
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	alert := &AlertJob{S: e.s, Free: func() (int64, error) { return 9999, nil }, Now: func() time.Time { return now }}
	metrics.Set("radius_nas_last_packet_timestamp", float64(now.Add(-time.Hour).Unix()), "nas", "10.9.0.7")
	run := func(what string, want int) {
		t.Helper()
		if err := alert.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if n := alertsFor(drain(got), "10.9.0.7"); n != want {
			t.Fatalf("%s: %d alerts, want %d", what, n, want)
		}
	}
	run("silent for an hour", 1)
	run("still silent", 0)
	metrics.Set("radius_nas_last_packet_timestamp", float64(now.Unix()), "nas", "10.9.0.7")
	run("packets back", 1)
	run("still fine", 0)
}

func TestJobFailingAlertOnceAndRecovery(t *testing.T) {
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	const name = "flaky-probe"

	// three failing runs in a row
	jctx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	calls := 0
	fail := func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 3 {
			cancel()
		}
		return errors.New("boom")
	}
	job.Run(jctx, name, time.Millisecond, fail)

	alert := &AlertJob{S: e.s, Free: func() (int64, error) { return 9999, nil }}
	if err := alert.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := alertsFor(drain(got), "gagal+3+kali"); n != 1 {
		t.Fatalf("job failing: %d alerts, want 1", n)
	}
	if err := alert.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := alertsFor(drain(got), "flaky"); n != 0 {
		t.Fatalf("alert repeated while still failing: %d", n)
	}

	// one successful run clears the streak: one recovery alert
	jctx, cancel = context.WithCancel(ctx)
	job.Run(jctx, name, time.Millisecond, func(context.Context) error { cancel(); return nil })
	if err := alert.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := alertsFor(drain(got), "flaky"); n != 1 {
		t.Fatalf("recovery: %d alerts, want 1", n)
	}
}

func TestBruteForceAlertOnceAndRecovery(t *testing.T) {
	metrics.Reset()
	t.Cleanup(func() { metrics.Now = time.Now; metrics.Reset() })
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	at := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	metrics.Now = func() time.Time { return at }
	metrics.Add("login_failures_total", 31, "scope", "portal")

	alert := &AlertJob{S: e.s, Free: func() (int64, error) { return 9999, nil }, Now: func() time.Time { return at }}
	if err := alert.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := alertsFor(drain(got), "Kegagalan+login"); n != 1 {
		t.Fatalf("31 failures: %d alerts, want 1", n)
	}
	if err := alert.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := alertsFor(drain(got), "login"); n != 0 {
		t.Fatalf("alert repeated: %d", n)
	}
	// 11 minutes later the failures have left the 10 minute window
	metrics.Now = func() time.Time { return at.Add(11 * time.Minute) }
	if err := alert.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := alertsFor(drain(got), "login"); n != 1 {
		t.Fatalf("recovery: %d alerts, want 1", n)
	}
}

// alert_channel=wa sends the operator alert through the WA server only.
func TestAlertChannelWhatsApp(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var mu sync.Mutex
	var bodies []string
	wa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.Path+" "+string(b))
		mu.Unlock()
		w.Write([]byte(`{"code":"SUCCESS"}`))
	}))
	t.Cleanup(wa.Close)
	for k, v := range map[string]string{"alert_channel": "wa", "alert_wa_to": "081234567890",
		"alt_wga_server_url": wa.URL, "telegram_bot": "T", "telegram_target_id": "1"} {
		if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := notify.Load(ctx, e.q)
	if err != nil {
		t.Fatal(err)
	}
	n.HTTP = wa.Client()
	n.TelegramAPI = "http://127.0.0.1:1" // must not be used
	e.s.Reload(n, time.UTC)

	if err := e.s.Alert(ctx, "disk penuh"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.HasPrefix(bodies[0], "/send/message") || !strings.Contains(bodies[0], "disk penuh") ||
		!strings.Contains(bodies[0], "081234567890@s.whatsapp.net") {
		t.Fatalf("WA sends = %v", bodies)
	}
}
