package web

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"testing"

	"golang.org/x/crypto/bcrypt"
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
