package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/service/apitoken"
)

type env struct {
	h    *Handlers
	svc  *apitoken.Service
	conn *sql.DB
	mux  *http.ServeMux
}

func setup(t *testing.T) *env {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	svc := &apitoken.Service{Q: q}
	h := New(q, svc)
	mux := http.NewServeMux()
	Register(mux, h)
	return &env{h, svc, conn, mux}
}

// token makes an admin of the role and a token for it.
func (e *env) token(t *testing.T, role string, ttl time.Duration) (string, db.ApiToken) {
	t.Helper()
	ctx := context.Background()
	a, err := e.h.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: role + time.Now().String(), PasswordHash: "secret-hash", Role: role})
	if err != nil {
		t.Fatal(err)
	}
	plain, row, err := e.svc.Create(ctx, a.ID, "t", ttl)
	if err != nil {
		t.Fatal(err)
	}
	return plain, row
}

func (e *env) get(path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	return w
}

func TestAuth(t *testing.T) {
	e := setup(t)
	if w := e.get("/api/v1/me", ""); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := e.get("/api/v1/me", "gbt_nope"); w.Code != 401 {
		t.Fatalf("bad token: %d", w.Code)
	}
	tok, row := e.token(t, "Admin", 0)
	w := e.get("/api/v1/me", tok)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"Admin"`) {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	if err := e.svc.Revoke(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	if w := e.get("/api/v1/me", tok); w.Code != 401 {
		t.Fatalf("revoked: %d", w.Code)
	}
	exp, _ := e.token(t, "Admin", time.Hour)
	if _, err := e.conn.Exec(`UPDATE api_tokens SET expires_at = 1 WHERE expires_at > 1`); err != nil {
		t.Fatal(err)
	}
	if w := e.get("/api/v1/me", exp); w.Code != 401 {
		t.Fatalf("expired: %d", w.Code)
	}
}

func TestCookieIgnored(t *testing.T) {
	e := setup(t)
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "anything"})
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("cookie only: %d", w.Code)
	}
}

func TestRoles(t *testing.T) {
	e := setup(t)
	for _, c := range []struct {
		role, path string
		code       int
	}{
		{"Report", "/api/v1/me", 200},
		{"Report", "/api/v1/customers", 403},
		{"Agent", "/api/v1/customers", 200},
		{"Agent", "/api/v1/nas", 403},
		{"Sales", "/api/v1/plans", 403},
		{"Agent", "/api/v1/radius/sessions", 403},
		{"Admin", "/api/v1/nas", 200},
		{"Admin", "/api/v1/routers", 200},
		{"SuperAdmin", "/api/v1/radius/sessions", 200},
		{"SuperAdmin", "/api/v1/subscriptions?customer_id=1", 200},
	} {
		tok, _ := e.token(t, c.role, 0)
		if w := e.get(c.path, tok); w.Code != c.code {
			t.Errorf("%s %s: got %d want %d (%s)", c.role, c.path, w.Code, c.code, w.Body)
		}
	}
}

func TestCustomersNoSecrets(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for _, u := range []string{"alice", "bob", "carol"} {
		if _, err := e.h.Q.CreateCustomer(ctx, db.CreateCustomerParams{Username: u, PasswordHash: "$2a$10$HASHVALUE", SecretEnc: []byte("enc-secret"), Status: "Active", ServiceType: "PPPoE"}); err != nil {
			t.Fatal(err)
		}
	}
	tok, _ := e.token(t, "Admin", 0)
	for _, p := range []string{"/api/v1/customers", "/api/v1/customers/1", "/api/v1/customers?q=ali"} {
		w := e.get(p, tok)
		if w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
		body := strings.ToLower(w.Body.String())
		for _, bad := range []string{"password", "secret", "hash"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s leaks %q: %s", p, bad, body)
			}
		}
	}
	if w := e.get("/api/v1/customers/99", tok); w.Code != 404 {
		t.Fatalf("missing customer: %d", w.Code)
	}
	var out struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(e.get("/api/v1/customers?q=ali", tok).Body.Bytes(), &out)
	if len(out.Data) != 1 {
		t.Fatalf("search: %v", out.Data)
	}
}

func TestPagination(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for _, u := range []string{"a", "b", "c"} {
		e.h.Q.CreateCustomer(ctx, db.CreateCustomerParams{Username: u, Status: "Active", ServiceType: "PPPoE"})
	}
	tok, _ := e.token(t, "Admin", 0)
	for _, p := range []string{"page=0", "page=x", "per_page=0", "per_page=101", "per_page=-1"} {
		if w := e.get("/api/v1/customers?"+p, tok); w.Code != 400 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := e.get("/api/v1/subscriptions", tok); w.Code != 400 {
		t.Errorf("no customer_id: %d", w.Code)
	}
	var out struct {
		Data    []any `json:"data"`
		HasMore bool  `json:"has_more"`
	}
	json.Unmarshal(e.get("/api/v1/customers?per_page=2", tok).Body.Bytes(), &out)
	if len(out.Data) != 2 || !out.HasMore {
		t.Fatalf("page 1: %d %v", len(out.Data), out.HasMore)
	}
	json.Unmarshal(e.get("/api/v1/customers?per_page=2&page=2", tok).Body.Bytes(), &out)
	if len(out.Data) != 1 || out.HasMore {
		t.Fatalf("page 2: %d %v", len(out.Data), out.HasMore)
	}
	json.Unmarshal(e.get("/api/v1/customers?per_page=100&page=9", tok).Body.Bytes(), &out)
	if out.Data == nil || len(out.Data) != 0 {
		t.Fatalf("past the end must be an empty list: %v", out.Data)
	}
	if w := e.get("/api/v1/customers?per_page=100", tok); w.Code != 200 {
		t.Errorf("per_page=100: %d", w.Code)
	}
}

func TestRateLimit(t *testing.T) {
	e := setup(t)
	tok, _ := e.token(t, "Admin", 0)
	for i := 0; i < rateLimit; i++ {
		if w := e.get("/api/v1/me", tok); w.Code != 200 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	if w := e.get("/api/v1/me", tok); w.Code != 429 {
		t.Fatalf("over limit: %d", w.Code)
	}
	other, _ := e.token(t, "Admin", 0)
	if w := e.get("/api/v1/me", other); w.Code != 200 {
		t.Fatalf("other token: %d", w.Code)
	}
}
