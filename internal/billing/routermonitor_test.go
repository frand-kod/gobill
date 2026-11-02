package billing

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/metrics"
)

func TestRouterCheckAlertsOncePerChange(t *testing.T) {
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	var fail error
	e.s.RouterFor = func(db.Router) (device.Router, error) {
		return device.Router{Exec: func(context.Context, []string) ([]map[string]string, error) {
			return []map[string]string{{"name": "Core"}}, fail
		}}, nil
	}
	check := func(want int, state int64, what string) {
		t.Helper()
		if err := e.s.RouterCheck(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(drain(got), "sendMessage"); n != want {
			t.Fatalf("%s: %d alerts, want %d", what, n, want)
		}
		r, _ := e.q.ListEnabledRouters(ctx)
		if !r[0].Online.Valid || r[0].Online.Int64 != state {
			t.Fatalf("%s: online = %+v, want %d", what, r[0].Online, state)
		}
	}
	check(0, 1, "first check, up")
	if r, _ := e.q.ListEnabledRouters(ctx); !r[0].LastSeenAt.Valid {
		t.Fatal("last_seen_at not stored")
	}
	check(0, 1, "still up")
	fail = errors.New("dial tcp: refused")
	check(0, 1, "one failed ping")
	fail = nil
	check(0, 1, "blip over, no recovery message")
	fail = errors.New("dial tcp: refused")
	check(0, 1, "first failure again")
	check(1, 0, "second failure declares offline")
	check(0, 0, "still down")
	check(0, 0, "still down again")
	fail = nil
	check(1, 1, "back up")
	check(0, 1, "still up, recovery sent once")

	// router_check = no switches the job off
	fail = errors.New("down")
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "router_check", Value: "no"}); err != nil {
		t.Fatal(err)
	}
	check(0, 1, "disabled")
}

func routerFailing(e *env, err *error) {
	e.s.RouterFor = func(db.Router) (device.Router, error) {
		return device.Router{Exec: func(context.Context, []string) ([]map[string]string, error) {
			return []map[string]string{{"name": "Core"}}, *err
		}}, nil
	}
}

// A router declared offline owns the incident: its NAS-silent and mass-drop alerts stay quiet.
func TestRouterOfflineSuppressesNASAndMassDrop(t *testing.T) {
	metrics.Reset()
	t.Cleanup(metrics.Reset)
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	fail := errors.New("timeout")
	routerFailing(e, &fail)
	now := time.Now().UTC().Truncate(time.Minute)
	ip := "h" // the test router's Host
	openSession(t, e, ip, "c1")
	for i := 0; i < 5; i++ {
		u := "d" + string(rune('a'+i))
		if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: u, Username: u, NasIp: ip,
			StartedAt: now.Unix() - 7200, UpdatedAt: now.Unix() - 60, StoppedAt: sql.NullInt64{Int64: now.Unix() - 60, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	metrics.Set("radius_nas_last_packet_timestamp", float64(now.Add(-time.Hour).Unix()), "nas", ip)
	alert := &AlertJob{S: e.s, Free: func() (int64, error) { return 9999, nil }, Now: func() time.Time { return now }}
	for i := 0; i < 2; i++ {
		if err := e.s.RouterCheck(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := alert.Run(ctx); err != nil {
		t.Fatal(err)
	}
	reqs := drain(got)
	if count(reqs, "tidak+terjangkau") != 1 || count(reqs, "NAS") != 0 || count(reqs, "massal") != 0 {
		t.Fatalf("want only the router message, got %v", reqs)
	}
	// router back: the NAS rules apply again
	fail = nil
	if err := e.s.RouterCheck(ctx); err != nil {
		t.Fatal(err)
	}
	if err := alert.Run(ctx); err != nil {
		t.Fatal(err)
	}
	reqs = drain(got)
	if count(reqs, "kembali+terhubung") != 1 || count(reqs, "NAS") != 1 {
		t.Fatalf("after recovery want router recovery and NAS silent, got %v", reqs)
	}
}

// A restart keeps the offline state: no repeated alert while down, the recovery still goes out.
func TestRouterOfflineStatePersistsAcrossRestart(t *testing.T) {
	e := setup(t)
	got := withNotify(t, e)
	ctx := context.Background()
	fail := errors.New("timeout")
	routerFailing(e, &fail)
	check := func(want int, what string) {
		t.Helper()
		if err := e.s.RouterCheck(ctx); err != nil {
			t.Fatal(err)
		}
		if n := count(drain(got), "sendMessage"); n != want {
			t.Fatalf("%s: %d alerts, want %d", what, n, want)
		}
	}
	check(0, "first failure")
	check(1, "declared offline")
	e.s.rt = routerState{} // restart
	check(0, "down after restart")
	check(0, "down after restart again")
	e.s.rt = routerState{}
	fail = nil
	check(1, "recovery after restart")
	if v, _ := e.q.GetSetting(ctx, routerDownKey); v != "[]" {
		t.Fatalf("offline set not cleared: %q", v)
	}
}
