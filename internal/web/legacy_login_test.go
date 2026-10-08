package web

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestLegacySha1LoginRehashes(t *testing.T) {
	s, _ := newTestApp(t)
	sum := sha1.Sum([]byte("secret123"))
	if _, err := s.conn.Exec("UPDATE admins SET password_hash = '!', legacy_sha1 = ? WHERE username = 'alice'", hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	if w := do(h, "POST", "/login", url.Values{"username": {"alice"}, "password": {"wrong"}}, nil); w.Code != 200 {
		t.Fatalf("wrong password: %d", w.Code)
	}
	login(t, h, "alice") // sha1 match
	var hash, legacy string
	s.conn.QueryRow("SELECT password_hash, legacy_sha1 FROM admins WHERE username = 'alice'").Scan(&hash, &legacy)
	if legacy != "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("secret123")) != nil {
		t.Fatalf("not rehashed: %q %q", hash, legacy)
	}
	login(t, h, "alice") // now plain bcrypt
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
	if w := do(s.Handler(), "POST", "/login", url.Values{"username": {"alice"}, "password": {"secret123"}}, nil); w.Code != 200 {
		t.Fatalf("old sha1 password still logs in: %d", w.Code)
	}
}
