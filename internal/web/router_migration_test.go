package web

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

// fakeLocal is a router with the hotspot users "ro" (a gobill customer) and "other" (router only).
type fakeLocal struct {
	mu       sync.Mutex
	password string
	disabled map[string]bool
}

func (f *fakeLocal) exec(sn []string) ([]map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch sn[0] {
	case "/ip/hotspot/user/print":
		row := func(id, name, pw string) map[string]string {
			return map[string]string{".id": id, "name": name, "password": pw, "disabled": strconv.FormatBool(f.disabled[id])}
		}
		return []map[string]string{row("*1", "ro", f.password), row("*2", "other", "x")}, nil
	case "/ip/hotspot/profile/print":
		return []map[string]string{{".id": "*P"}}, nil
	case "/ip/hotspot/user/set":
		f.disabled[strings.TrimPrefix(sn[1], "=numbers=")] = sn[2] == "=disabled=yes"
	}
	return nil, nil
}

// migApp has router r1 and an active hotspot subscription for customer "ro" (password pw12345).
func migApp(t *testing.T) (h http.Handler, c *http.Cookie, q *db.Queries, base string, f *fakeLocal, sent func() []string) {
	f = &fakeLocal{password: "pw12345", disabled: map[string]bool{}}
	h, c, q, rid, sent := pushApp(t, f.exec)
	ctx := t.Context()
	addPlans(t, q, 1, "MikrotikHotspot", "hs")
	plans, _ := q.ListPlansByRouter(ctx, sql.NullInt64{Int64: 1, Valid: true})
	enc, _ := secret.Seal(bytes.Repeat([]byte{7}, 32), []byte("pw12345"))
	cu, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "ro", PasswordHash: "h", Fullname: "ro", ServiceType: "Hotspot", SecretEnc: enc, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: cu.ID, PlanID: plans[0].ID, RouterID: sql.NullInt64{Int64: 1, Valid: true},
		Type: "Hotspot", StartedAt: now, ExpiresAt: now + 3600, Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	return h, c, q, "/admin/routers/" + rid + "/migration", f, sent
}

func migStep(t *testing.T, q *db.Queries) string {
	rows, _ := q.ListSettings(t.Context())
	for _, r := range rows {
		if strings.HasPrefix(r.Key, "radius_migration_") {
			return r.Value[:1]
		}
	}
	return ""
}

func TestRouterMigrationSteps(t *testing.T) {
	h, c, q, base, f, sent := migApp(t)
	post := func(act string, kv ...string) int {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return do(h, "POST", base+"/"+act, v, c).Code
	}
	changes := func(sub string) int { // commands that wrote something matching sub
		n := 0
		for _, s := range sent() {
			if strings.Contains(s, sub) {
				n++
			}
		}
		return n
	}
	confirm := []string{"confirm", "1"}

	// the page lists the account, never the passwords, and shows the walled garden block
	body := do(h, "GET", base, nil, c).Body.String()
	if !strings.Contains(body, "Hotspot: ro") || strings.Contains(body, "pw12345") || !strings.Contains(body, "walled-garden") {
		t.Fatalf("page: %s", body)
	}
	if strings.Contains(body, base+"/switch") {
		t.Fatal("step 2 offered before step 1")
	}

	// steps 2 and 3 are refused before their turn, nothing is written
	for _, act := range []string{"switch", "disable"} {
		if post(act, confirm...) != 303 {
			t.Fatalf("%s: want redirect", act)
		}
	}
	if changes("use-radius") != 0 || changes("/set") != 0 || migStep(t, q) != "" {
		t.Fatalf("refused steps wrote: %v", sent())
	}

	// step 1: a wrong router password fails and does not unlock step 2
	f.password = "other-pw"
	post("test", "account", "Hotspot:ro")
	if post("switch", confirm...) != 303 || changes("use-radius") != 0 || migStep(t, q) != "" {
		t.Fatalf("failed test unlocked step 2: %v", sent())
	}
	// a matching password passes and the result is shown once
	f.password = "pw12345"
	post("test", "account", "Hotspot:ro")
	if b := do(h, "GET", base, nil, c).Body.String(); !strings.Contains(b, "Hotspot:ro") || !strings.Contains(b, base+"/switch") || migStep(t, q) != "1" {
		t.Fatalf("test result: step %q %s", migStep(t, q), b)
	}
	if changes("/set") != 0 {
		t.Fatalf("the test changed the router: %v", sent())
	}

	// step 3 is still refused; step 2 shows the exact change first, applies only when confirmed
	post("disable", confirm...)
	if changes("/set") != 0 {
		t.Fatal("step 3 ran before step 2")
	}
	w := do(h, "POST", base+"/switch", url.Values{}, c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/ip hotspot profile set [find] use-radius=yes") || changes("use-radius") != 0 {
		t.Fatalf("switch preview: %d %s", w.Code, w.Body.String())
	}
	post("switch", confirm...)
	if changes("/ip/hotspot/profile/set =numbers=*P =use-radius=yes") != 1 || migStep(t, q) != "2" {
		t.Fatalf("switch: step %q %v", migStep(t, q), sent())
	}

	// step 3 shows the count (1: "other" is not a gobill customer and is left alone)
	w = do(h, "POST", base+"/disable", url.Values{}, c)
	if !strings.Contains(w.Body.String(), "dinonaktifkan 1 (") || changes("disabled=yes") != 0 {
		t.Fatalf("disable preview: %s", w.Body.String())
	}
	post("disable", confirm...)
	if !f.disabled["*1"] || f.disabled["*2"] || migStep(t, q) != "3" {
		t.Fatalf("disable: %v step %q", f.disabled, migStep(t, q))
	}

	// rollback: the account is enabled again, use-radius off, state reset
	post("rollback")
	if f.disabled["*1"] || changes("=use-radius=no") != 1 || migStep(t, q) != "0" {
		t.Fatalf("rollback: %v step %q %v", f.disabled, migStep(t, q), sent())
	}

	// no step removed anything or touched a session
	for _, s := range sent() {
		l := strings.ToLower(s)
		if strings.Contains(l, "remove") || strings.Contains(l, "active") || strings.Contains(l, "disconnect") || strings.Contains(l, "kick") {
			t.Fatalf("a step cut a session or removed something: %s", s)
		}
	}
}

func TestRouterMigrationRollbackAtStepOne(t *testing.T) {
	h, c, q, base, _, sent := migApp(t)
	do(h, "POST", base+"/test", url.Values{"account": {"Hotspot:ro"}}, c)
	if migStep(t, q) != "1" {
		t.Fatalf("step %q", migStep(t, q))
	}
	do(h, "POST", base+"/rollback", url.Values{}, c)
	if migStep(t, q) != "0" {
		t.Fatal("rollback at step 1 did not reset")
	}
	for _, s := range sent() {
		if strings.Contains(s, "/set") {
			t.Fatalf("rollback at step 1 changed the router: %s", s)
		}
	}
}

func TestRouterMigrationRadiusOnlyRefused(t *testing.T) {
	h, c, q, rid, sent := pushApp(t, (&fakeLocal{disabled: map[string]bool{}}).exec)
	addPlans(t, q, 1, "Radius", "rp")
	base := "/admin/routers/" + rid + "/migration"
	wantCode(t, do(h, "GET", base, nil, c), 200, "page")
	wantCode(t, do(h, "POST", base+"/test", url.Values{"account": {"Hotspot:ro"}}, c), 303, "test")
	if b := do(h, "GET", "/admin/routers", nil, c).Body.String(); !strings.Contains(b, "mode RADIUS") {
		t.Fatalf("message: %s", b)
	}
	if migStep(t, q) != "" {
		t.Fatal("state stored")
	}
	for _, s := range sent() {
		if strings.Contains(s, "/set") {
			t.Fatalf("wrote: %s", s)
		}
	}
}

func TestRouterMigrationNoLocalAccounts(t *testing.T) {
	h, c, q, rid, _ := pushApp(t, (&fakeLocal{disabled: map[string]bool{}}).exec)
	addPlans(t, q, 1, "MikrotikHotspot", "hs") // a plan but no subscription: nothing to migrate
	do(h, "POST", "/admin/routers/"+rid+"/migration/test", url.Values{"account": {"Hotspot:ro"}}, c)
	if migStep(t, q) != "" {
		t.Fatal("state stored without accounts")
	}
}
