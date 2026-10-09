package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
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
	check(1, 0, "went down")
	check(0, 0, "still down")
	check(0, 0, "still down again")
	fail = nil
	check(1, 1, "back up")

	// router_check = no switches the job off
	fail = errors.New("down")
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "router_check", Value: "no"}); err != nil {
		t.Fatal(err)
	}
	check(0, 1, "disabled")
}
