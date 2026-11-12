package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestAPIAccessPage(t *testing.T) {
	s, q := newTestApp(t)
	s.Lang.Store("english")
	h := s.Handler()
	ctx := t.Context()
	alice, rita := login(t, h, "alice"), login(t, h, "rita")
	agent, err := q.CreateAdmin(ctx, db.CreateAdminParams{Username: "agnes", PasswordHash: "x", Role: "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	api := func(path, tok string) int {
		return do(h, "GET", path, nil, nil, "Authorization", "Bearer "+tok).Code
	}

	// not SuperAdmin: refused, and nothing is created
	if c := do(h, "GET", "/admin/api", nil, rita).Code; c != http.StatusForbidden && c != http.StatusSeeOther {
		t.Fatalf("report GET: %d", c)
	}
	if c := do(h, "POST", "/admin/api/tokens", url.Values{"name": {"x"}, "admin_id": {"1"}, "ttl": {"0"}}, rita).Code; c == http.StatusOK {
		t.Fatalf("report POST: %d", c)
	}
	if c := do(h, "GET", "/admin/api", nil, nil).Code; c == http.StatusOK {
		t.Fatalf("anonymous GET: %d", c)
	}
	if !strings.Contains(do(h, "GET", "/admin/api", nil, alice).Body.String(), "Create token") {
		t.Fatal("superadmin page")
	}

	// create: the token is in this response only
	form := url.Values{"name": {"billing bot"}, "admin_id": {strconv.FormatInt(agent.ID, 10)}, "ttl": {"30"}}
	w := do(h, "POST", "/admin/api/tokens", form, alice)
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("create: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	m := regexp.MustCompile(`gbt_[A-Za-z0-9_-]{43}`).FindString(w.Body.String())
	if m == "" {
		t.Fatal("token not shown")
	}
	if strings.Contains(do(h, "GET", "/admin/api", nil, alice).Body.String(), m) {
		t.Fatal("token shown twice")
	}
	if bad := do(h, "POST", "/admin/api/tokens", url.Values{"name": {""}, "admin_id": {"1"}, "ttl": {"0"}}, alice); bad.Code != http.StatusBadRequest {
		t.Fatalf("empty name: %d", bad.Code)
	}

	// the API is off by default
	if c := api("/api/v1/me", m); c != http.StatusForbidden {
		t.Fatalf("default off: %d", c)
	}
	on := url.Values{"api_enabled": {"yes"}, "roles_customers": {"Admin", "Agent"}, "roles_network": {"Admin"}}
	wantCode(t, do(h, "POST", "/admin/api/settings", on, alice), http.StatusSeeOther, "save")
	if c := api("/api/v1/customers", m); c != http.StatusOK {
		t.Fatalf("agent customers: %d", c)
	}
	if c := api("/api/v1/plans", m); c != http.StatusForbidden {
		t.Fatalf("agent plans: %d", c)
	}

	// removing Agent from customers takes effect at once
	on["roles_customers"] = []string{"Admin"}
	wantCode(t, do(h, "POST", "/admin/api/settings", on, alice), http.StatusSeeOther, "save 2")
	if c := api("/api/v1/customers", m); c != http.StatusForbidden {
		t.Fatalf("agent removed: %d", c)
	}

	// an unknown role is refused and nothing changes
	on["roles_customers"] = []string{"Admin", "Agent", "Root"}
	wantCode(t, do(h, "POST", "/admin/api/settings", on, alice), http.StatusBadRequest, "unknown role")
	if c := api("/api/v1/customers", m); c != http.StatusForbidden {
		t.Fatalf("after unknown role: %d", c)
	}

	// revoke
	var id int64
	if err := s.ax.Conn.QueryRow(`SELECT id FROM api_tokens`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if c := api("/api/v1/me", m); c != http.StatusOK {
		t.Fatalf("me before revoke: %d", c)
	}
	wantCode(t, do(h, "POST", "/admin/api/tokens/"+strconv.FormatInt(id, 10)+"/revoke", url.Values{}, alice), http.StatusSeeOther, "revoke")
	if c := api("/api/v1/me", m); c != http.StatusUnauthorized {
		t.Fatalf("me after revoke: %d", c)
	}

	// switch off
	wantCode(t, do(h, "POST", "/admin/api/settings", url.Values{}, alice), http.StatusSeeOther, "off")
	if c := api("/api/v1/me", m); c != http.StatusForbidden {
		t.Fatalf("switched off: %d", c)
	}

	// log entries never hold the token
	rows, err := s.ax.Conn.Query(`SELECT action, description FROM activity_logs WHERE action LIKE 'api.%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var a, d string
		rows.Scan(&a, &d)
		n++
		if strings.Contains(d, "gbt_") {
			t.Fatalf("token in log: %s", d)
		}
	}
	if n < 4 {
		t.Fatalf("log entries: %d", n)
	}
}
