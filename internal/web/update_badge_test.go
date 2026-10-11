package web

import (
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

func TestUpdateBadgeForSuperAdminOnly(t *testing.T) {
	s, q := newTestApp(t)
	s.lang.Store("english")
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if _, err := q.CreateAdmin(t.Context(), db.CreateAdminParams{Username: "bob", Fullname: "Bob", PasswordHash: string(hash), Role: "Admin"}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	alice, bob := login(t, h, "alice"), login(t, h, "bob")
	const badge = "v0.1.7 available"
	tag := "v0.1.7"
	s.UpdateInfo = func() (string, string) { return tag, "https://github.com/frand-kod/gobill/releases/tag/v0.1.7" }

	s.Version = "v0.1.6"
	w := do(h, "GET", "/admin", nil, alice)
	wantCode(t, w, http.StatusOK, "dashboard for SuperAdmin")
	if !strings.Contains(w.Body.String(), badge) || !strings.Contains(w.Body.String(), "/admin/docs/upgrade") {
		t.Fatal("SuperAdmin does not see the update badge")
	}
	w = do(h, "GET", "/admin", nil, bob)
	wantCode(t, w, http.StatusOK, "dashboard for Admin")
	if strings.Contains(w.Body.String(), badge) {
		t.Fatal("Admin role sees the update badge")
	}

	s.Version = "v0.1.7" // up to date
	if w := do(h, "GET", "/admin", nil, alice); strings.Contains(w.Body.String(), badge) {
		t.Fatal("badge shown when up to date")
	}
	s.Version = "dev" // dev builds never show it
	if w := do(h, "GET", "/admin", nil, alice); strings.Contains(w.Body.String(), badge) {
		t.Fatal("badge shown for a dev build")
	}
}
