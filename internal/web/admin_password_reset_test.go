package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

// SuperAdmin "Reset password" on the admin edit page: a random password shown once, the admin's
// sessions end, the activity log has no password, and nobody else may do it.
func TestAdminPasswordReset(t *testing.T) {
	_, h, q, alice := crudApp(t)
	ctx := t.Context()
	vic := mk(t, q, "vic", "Admin", 0)
	vicCookie := login(t, h, "vic")
	mk(t, q, "boss2", "Admin", 0)
	boss2Cookie := login(t, h, "boss2")
	path := "/admin/users/" + itoa(vic.ID) + "/password/reset"

	// the button: SuperAdmin sees it for another admin, an Admin does not
	if b := do(h, "GET", "/admin/users/"+itoa(vic.ID)+"/edit", nil, alice).Body.String(); !strings.Contains(b, `formaction="`+path+`"`) {
		t.Fatal("SuperAdmin edit page lacks the reset button")
	}
	if b := do(h, "GET", "/admin/users/"+itoa(vic.ID)+"/edit", nil, boss2Cookie).Body.String(); strings.Contains(b, "/password/reset") {
		t.Error("Admin sees the reset button")
	}

	// roles: only SuperAdmin may reset
	wantCode(t, do(h, "POST", path, nil, boss2Cookie), 403, "reset by Admin")
	if w := do(h, "GET", "/admin", nil, vicCookie); w.Code != 200 {
		t.Fatalf("victim session before reset: %d", w.Code)
	}

	// SuperAdmin resets: the new password is on the page once
	w := do(h, "POST", path, nil, alice)
	m := regexp.MustCompile(`id="new-pw"[^>]*value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil {
		t.Fatalf("reset page: %d", w.Code)
	}
	pw := m[1]
	if strings.Count(w.Body.String(), pw) < 1 || len(pw) != 16 {
		t.Fatalf("password %q", pw)
	}
	after, _ := q.GetAdmin(ctx, vic.ID)
	if bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte(pw)) != nil {
		t.Error("new password does not match the stored hash")
	}
	if after.SessionVersion != vic.SessionVersion+1 {
		t.Errorf("session_version %d -> %d", vic.SessionVersion, after.SessionVersion)
	}

	// the admin's old session is ended: back to the login page
	if w := do(h, "GET", "/admin", nil, vicCookie); w.Code != http.StatusSeeOther {
		t.Errorf("old session still works: %d", w.Code)
	}

	// the activity log names the admin and never the password
	logs, _ := q.ListActivityLogs(ctx, db.ListActivityLogsParams{Limit: 50})
	found := false
	for _, l := range logs {
		if strings.Contains(l.Description, pw) || strings.Contains(l.Action, pw) {
			t.Fatalf("password in activity log: %+v", l)
		}
		found = found || (l.Action == "users.password.reset" && l.Description == "vic")
	}
	if !found {
		t.Error("no activity log entry for the reset")
	}

	// own account goes through Change Password instead: refused, password unchanged
	root, _ := q.GetAdminByUsername(ctx, "alice")
	before, _ := q.GetAdmin(ctx, root.ID)
	w = do(h, "POST", "/admin/users/"+itoa(root.ID)+"/password/reset", url.Values{}, alice)
	if w.Code != http.StatusSeeOther {
		t.Errorf("self reset: %d", w.Code)
	}
	if now, _ := q.GetAdmin(ctx, root.ID); now.PasswordHash != before.PasswordHash {
		t.Error("self reset changed the password")
	}
}
