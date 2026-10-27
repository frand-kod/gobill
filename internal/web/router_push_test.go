package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

// pushApp is a web app whose router answers through exec; sent lists the commands that reached it.
func pushApp(t *testing.T, exec func([]string) ([]map[string]string, error)) (h http.Handler, c *http.Cookie, q *db.Queries, rid string, sent func() []string) {
	s, h, q, c := crudApp(t)
	syncs.mu.Lock()
	syncs.runs = map[int64]*syncRun{}
	syncs.mu.Unlock()
	var mu sync.Mutex
	var cmds []string
	s.Billing = &billing.Service{DB: s.conn, Q: q, Key: s.SecretKey,
		RouterFor: func(db.Router) (device.Router, error) {
			return device.Router{Exec: func(_ context.Context, sn []string) ([]map[string]string, error) {
				mu.Lock()
				cmds = append(cmds, strings.Join(sn, " "))
				mu.Unlock()
				return exec(sn)
			}}, nil
		}}
	rt, err := q.CreateRouter(t.Context(), db.CreateRouterParams{Name: "r1", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	return h, c, q, itoa(rt.ID), func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), cmds...) }
}

func addPlans(t *testing.T, q *db.Queries, rid int64, dev string, names ...string) {
	bw, _ := q.CreateBandwidth(t.Context(), db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	for _, n := range names {
		if _, err := q.CreatePlan(t.Context(), db.CreatePlanParams{Name: n, Type: "Hotspot", Billing: "prepaid", Price: 1, Validity: 1, ValidityUnit: "Days",
			Enabled: 1, Device: dev, BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, RouterID: sql.NullInt64{Int64: rid, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
}

// waitStatus polls the status page until it contains want, with a deadline.
func waitStatus(t *testing.T, h http.Handler, c *http.Cookie, path, want string) string {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if body := do(h, "GET", path, nil, c).Body.String(); strings.Contains(body, want) {
			return body
		}
	}
	t.Fatalf("status page never showed %q", want)
	return ""
}

func TestRouterPushAllBackground(t *testing.T) {
	gate := make(chan struct{})
	var mu sync.Mutex
	var down error
	h, c, q, rid, sent := pushApp(t, func(sn []string) ([]map[string]string, error) {
		if sn[0] == "/system/identity/print" {
			mu.Lock()
			defer mu.Unlock()
			return []map[string]string{{"name": "Core"}}, down
		}
		<-gate // an item: held until the test lets it go
		return nil, errors.New("profile rejected")
	})
	addPlans(t, q, 1, "MikrotikHotspot", "p1", "p2")
	status := "/admin/routers/" + rid + "/sync"

	// the click returns at once, while the first item is still held, and lands on the status page
	w := do(h, "POST", status, nil, c)
	wantCode(t, w, 303, "start")
	if loc := w.Header().Get("Location"); loc != status {
		t.Fatalf("redirect to %q", loc)
	}
	// a second click during the run is refused with a message, not queued
	wantCode(t, do(h, "POST", status, nil, c), 303, "second start")
	running := do(h, "GET", status, nil, c)
	if b := running.Body.String(); !strings.Contains(b, "masih berjalan") || !strings.Contains(b, "Berjalan") || running.Header().Get("Refresh") == "" {
		t.Fatalf("running page: %s", b)
	}
	close(gate)

	// finished: counts and one failure line per plan with its reason
	done := waitStatus(t, h, c, status, "Selesai")
	for _, want := range []string{"2 / 2", "plan p1: profile rejected", "plan p2: profile rejected"} {
		if !strings.Contains(done, want) {
			t.Fatalf("missing %q: %s", want, done)
		}
	}
	if w := do(h, "GET", status, nil, c); w.Header().Get("Refresh") != "" {
		t.Fatal("finished page still refreshes")
	}

	// offline: the ping fails, the run ends stopped and no item is tried
	mu.Lock()
	down = errors.New("dial tcp: refused")
	mu.Unlock()
	before := len(sent())
	wantCode(t, do(h, "POST", status, nil, c), 303, "offline start")
	stopped := waitStatus(t, h, c, status, "Dihentikan")
	if !strings.Contains(stopped, "router offline") || len(sent()) != before+1 {
		t.Fatalf("offline: %d commands, %s", len(sent())-before, stopped)
	}
}

func TestRouterPushAllRadiusRefused(t *testing.T) {
	h, c, q, rid, sent := pushApp(t, func([]string) ([]map[string]string, error) { return nil, nil })
	addPlans(t, q, 1, "Radius", "rp")
	status := "/admin/routers/" + rid + "/sync"
	wantCode(t, do(h, "POST", status, nil, c), 303, "radius start")
	if body := do(h, "GET", status, nil, c).Body.String(); !strings.Contains(body, "mode RADIUS") || !strings.Contains(body, "Belum ada dorongan") {
		t.Fatalf("radius not refused or a run exists: %s", body)
	}
	if len(sent()) != 0 {
		t.Fatalf("router was contacted: %v", sent())
	}
}

func TestRouterListLastPushLink(t *testing.T) {
	h, c, q, rid, _ := pushApp(t, func([]string) ([]map[string]string, error) { return nil, nil })
	// rid (r1) has no run; r2 gets one
	r2, err := q.CreateRouter(t.Context(), db.CreateRouterParams{Name: "r2", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	syncs.mu.Lock()
	syncs.runs[r2.ID] = &syncRun{}
	syncs.mu.Unlock()
	body := do(h, "GET", "/admin/routers", nil, c).Body.String()
	// the default language is Indonesian, so the label is "Dorongan terakhir" (Last push)
	if !strings.Contains(body, `href="/admin/routers/`+itoa(r2.ID)+`/sync">Dorongan terakhir: r2</a>`) {
		t.Fatalf("router with a run has no link: %s", body)
	}
	if strings.Contains(body, `href="/admin/routers/`+rid+`/sync"`) || strings.Contains(body, "Dorongan terakhir: r1") {
		t.Fatalf("router without a run has a link: %s", body)
	}
}
