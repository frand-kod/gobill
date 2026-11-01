package web

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

func TestLegacySha1LoginRehashes(t *testing.T) {
	s, _ := newTestApp(t)
	sum := sha1.Sum([]byte("secret123"))
	if _, err := s.conn.Exec("UPDATE admins SET password_hash = '!', legacy_sha1 = ? WHERE username = 'alice'", hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	if w := do(h, "POST", "/admin/login", url.Values{"username": {"alice"}, "password": {"wrong"}}, nil); w.Code != 200 {
		t.Fatalf("wrong password: %d", w.Code)
	}
	login(t, h, "alice") // sha1 match
	var hash, legacy string
	s.conn.QueryRow("SELECT password_hash, legacy_sha1 FROM admins WHERE username = 'alice'").Scan(&hash, &legacy)
	if legacy == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("secret123")) != nil {
		t.Fatalf("not rehashed, or the marker was cleared before a password change: %q %q", hash, legacy)
	}
	c := login(t, h, "alice") // now plain bcrypt; the marker stays
	// A2: until the password is changed, every admin page redirects to the change-password page.
	for _, p := range []string{"/admin", "/admin/customers"} {
		if w := do(h, "GET", p, nil, c); w.Code != 303 || w.Header().Get("Location") != "/admin/password" {
			t.Fatalf("%s: %d %q, want redirect to /admin/password", p, w.Code, w.Header().Get("Location"))
		}
	}
	if w := do(h, "GET", "/admin/password", nil, c); w.Code != 200 {
		t.Fatalf("change-password page: %d", w.Code)
	}
	w := do(h, "POST", "/admin/password", url.Values{"current": {"secret123"}, "password": {"brand-new-pass-1"}, "cpassword": {"brand-new-pass-1"}}, c)
	if w.Code >= 400 {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}
	s.conn.QueryRow("SELECT legacy_sha1 FROM admins WHERE username = 'alice'").Scan(&legacy)
	if legacy != "" {
		t.Fatal("marker still set after the password change")
	}
}

func TestAdminResetClearsLegacySha1(t *testing.T) {
	s, _ := newTestApp(t)
	sum := sha1.Sum([]byte("secret123"))
	s.conn.Exec("UPDATE admins SET password_hash = '!', legacy_sha1 = ? WHERE username = 'alice'", hex.EncodeToString(sum[:]))
	var id int64
	s.conn.QueryRow("SELECT id FROM admins WHERE username = 'alice'").Scan(&id)
	hash, _ := bcrypt.GenerateFromPassword([]byte("newpass1"), bcrypt.MinCost)
	if _, err := s.queries.SetAdminPassword(t.Context(), db.SetAdminPasswordParams{PasswordHash: string(hash), ID: id}); err != nil {
		t.Fatal(err)
	}
	if w := do(s.Handler(), "POST", "/admin/login", url.Values{"username": {"alice"}, "password": {"secret123"}}, nil); w.Code != 200 {
		t.Fatalf("old sha1 password still logs in: %d", w.Code)
	}
}
