package web

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestRouterReconcileScreen(t *testing.T) {
	var mu sync.Mutex
	var down error
	h, c, q, rid, _ := pushApp(t, func(sn []string) ([]map[string]string, error) {
		mu.Lock()
		defer mu.Unlock()
		switch sn[0] {
		case "/ppp/secret/print":
			return []map[string]string{{"name": "ro", "password": "SECRET-RO", "profile": "gold"}}, down
		case "/ip/hotspot/user/print":
			return nil, down
		}
		return []map[string]string{{"name": "Core"}}, down
	})
	path := "/admin/routers/" + rid + "/reconcile"
	ctx := t.Context()
	count := func() int { c, _ := q.GetCustomerByUsername(ctx, "ro"); return int(c.ID) } // 0 = none

	// GET lists the row with its default (leave) and never shows the router password
	w := do(h, "GET", path, nil, c)
	wantCode(t, w, 200, "get")
	if b := w.Body.String(); !strings.Contains(b, "ro (PPPoE)") || strings.Contains(b, "SECRET-RO") {
		t.Fatalf("page: %s", b)
	}
	// preview with import chosen: summary only, nothing created
	choice := url.Values{"a:PPPoE:ro": {"import"}}
	w = do(h, "POST", path, choice, c)
	if b := w.Body.String(); w.Code != 200 || !strings.Contains(b, `name="confirm"`) || strings.Contains(b, "SECRET-RO") || count() != 0 {
		t.Fatalf("preview: %d %s", w.Code, b)
	}
	// confirm applies the choice; the result is a redirect with a flash
	choice.Set("confirm", "1")
	wantCode(t, do(h, "POST", path, choice, c), 303, "confirm")
	if count() == 0 {
		t.Fatal("customer not imported")
	}
	if b := do(h, "GET", "/admin/routers", nil, c).Body.String(); !strings.Contains(b, "Diimpor 1") || strings.Contains(b, "SECRET-RO") {
		t.Fatalf("result: %s", b)
	}

	// offline: no write, a message, no new customer
	mu.Lock()
	down = errors.New("dial tcp: i/o timeout")
	mu.Unlock()
	q.DeleteCustomer(ctx, int64(count()))
	wantCode(t, do(h, "POST", path, choice, c), 303, "offline")
	if count() != 0 {
		t.Fatal("offline run imported")
	}
	if b := do(h, "GET", "/admin/routers", nil, c).Body.String(); !strings.Contains(b, "tidak ada yang diubah") {
		t.Fatalf("offline message: %s", b)
	}

	// RADIUS-only router: refused
	mu.Lock()
	down = nil
	mu.Unlock()
	addPlans(t, q, 1, "Radius", "rp")
	wantCode(t, do(h, "POST", path, choice, c), 303, "radius")
	if b := do(h, "GET", "/admin/routers", nil, c).Body.String(); !strings.Contains(b, "mode RADIUS") || count() != 0 {
		t.Fatalf("radius: %s", b)
	}
}
