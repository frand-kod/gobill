package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

// newTestServer returns a handler over a temp DB with admin "alice"/"secret123" (SuperAdmin)
// and "rita"/"secret123" (Report).
func newTestServer(t *testing.T) (http.Handler, *db.Queries) {
	s, q := newTestApp(t)
	return s.Handler(), q
}

func newTestApp(t *testing.T) (*Server, *db.Queries) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	for _, a := range []db.CreateAdminParams{
		{Username: "alice", Fullname: "Alice Wong", PasswordHash: string(hash), Role: "SuperAdmin"},
		{Username: "rita", Fullname: "Rita", PasswordHash: string(hash), Role: "Report"},
	} {
		if _, err := q.CreateAdmin(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(conn, false)
	if err != nil {
		t.Fatal(err)
	}
	return s, q
}

func do(h http.Handler, method, target string, form url.Values, cookie *http.Cookie, hdr ...string) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, h http.Handler, user string) *http.Cookie {
	t.Helper()
	w := do(h, "POST", "/login", url.Values{"username": {user}, "password": {"secret123"}}, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("login %s: got %d %q", user, w.Code, w.Header().Get("Location"))
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "nuxbill_session" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestAdminNeedsLogin(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(h, "GET", "/admin", nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("got %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestLogin(t *testing.T) {
	h, _ := newTestServer(t)
	for _, user := range []string{"alice", "nobody"} {
		w := do(h, "POST", "/login", url.Values{"username": {user}, "password": {"wrong"}}, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Nama pengguna atau kata sandi salah") {
			t.Fatalf("%s wrong password: %d %s", user, w.Code, w.Body.String())
		}
	}
	c := login(t, h, "alice")
	w := do(h, "GET", "/admin", nil, c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Alice Wong") {
		t.Fatalf("dashboard: %d", w.Code)
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(h, "POST", "/login", url.Values{"username": {"alice"}, "password": {"secret123"}}, nil,
		"Sec-Fetch-Site", "cross-site")
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d", w.Code)
	}
}

func TestReportRoleForbiddenOnSettings(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "rita")
	if w := do(h, "GET", "/admin", nil, c); w.Code != 200 {
		t.Fatalf("dashboard: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/settings", nil, c); w.Code != http.StatusForbidden {
		t.Fatalf("got %d", w.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	h, _ := newTestServer(t)
	bad := url.Values{"username": {"alice"}, "password": {"wrong"}}
	for i := 0; i < 10; i++ {
		if w := do(h, "POST", "/login", bad, nil); w.Code != 200 {
			t.Fatalf("attempt %d: %d", i+1, w.Code)
		}
	}
	if w := do(h, "POST", "/login", bad, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("11th: %d", w.Code)
	}
}

func TestEveryPageRenders(t *testing.T) {
	h, _ := newTestServer(t)
	if w := do(h, "GET", "/login", nil, nil); w.Code != 200 {
		t.Fatalf("/login: %d", w.Code)
	}
	c := login(t, h, "alice")
	for _, p := range []string{"/admin", "/admin/settings/app"} {
		if w := do(h, "GET", p, nil, c); w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	if w := do(h, "GET", "/static/app.css", nil, nil); w.Code != 200 {
		t.Fatalf("css: %d", w.Code)
	}
}

func TestClockBanner(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	c := login(t, h, "alice")
	if w := do(h, "GET", "/admin", nil, c); strings.Contains(w.Body.String(), "NTP") {
		t.Fatal("banner without warning")
	}
	s.ClockWarning = func() string { return "clock not synced with NTP" }
	if w := do(h, "GET", "/admin", nil, c); !strings.Contains(w.Body.String(), "clock not synced with NTP") {
		t.Fatal("banner missing")
	}
}
