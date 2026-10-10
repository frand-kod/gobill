package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

// mk creates an admin with password "secret123" (alice=SuperAdmin id 1, rita=Report id 2 exist).
func mk(t *testing.T, q *db.Queries, user, role string, root int64) db.Admin {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	p := db.CreateAdminParams{Username: user, Fullname: user + " name", PasswordHash: string(h), Role: role}
	if root != 0 {
		p.RootID.Int64, p.RootID.Valid = root, true
	}
	a, err := q.CreateAdmin(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func uform(user, role string) url.Values {
	return url.Values{"username": {user}, "fullname": {user + " name"}, "role": {role}, "status": {"Active"},
		"password": {"newpass1"}, "cpassword": {"newpass1"}}
}

func byName(t *testing.T, q *db.Queries, name string) db.Admin {
	t.Helper()
	a, err := q.GetAdminByUsername(t.Context(), name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return a
}

func TestAdminCreateAndPassword(t *testing.T) {
	_, h, q, c := crudApp(t)
	f := uform("newbie", "Admin")
	f.Set("password", "abc")
	f.Set("cpassword", "abc")
	wantCode(t, do(h, "POST", "/admin/users", f, c), 422, "short password")
	f.Set("password", "")
	f.Set("cpassword", "")
	wantCode(t, do(h, "POST", "/admin/users", f, c), 422, "password required on create")
	f.Set("password", "newpass1")
	f.Set("cpassword", "other")
	wantCode(t, do(h, "POST", "/admin/users", f, c), 422, "mismatch")
	f.Set("cpassword", "newpass1")
	wantCode(t, do(h, "POST", "/admin/users", f, c), 303, "create")
	a := byName(t, q, "newbie")
	if a.Role != "Admin" || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte("newpass1")) != nil ||
		!strings.HasPrefix(a.PasswordHash, "$2") {
		t.Fatalf("stored: %+v", a)
	}
	wantCode(t, do(h, "POST", "/admin/users", f, c), 422, "duplicate username")
	// empty password on edit keeps the hash and the sessions
	e := uform("newbie", "Admin")
	e.Set("password", "")
	e.Set("cpassword", "")
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(a.ID), e, c), 303, "edit")
	if b := byName(t, q, "newbie"); b.PasswordHash != a.PasswordHash || b.SessionVersion != a.SessionVersion {
		t.Fatal("hash or session version changed without cause")
	}
	logs, _ := q.ListActivityLogs(t.Context(), db.ListActivityLogsParams{Limit: 10})
	if len(logs) != 2 || logs[1].Action != "users.create" || logs[0].Action != "users.update" {
		t.Fatalf("logs: %+v", logs)
	}
}

func TestAdminRoleRules(t *testing.T) {
	_, h, q, c := crudApp(t)
	adm := mk(t, q, "adm", "Admin", 0)
	adm2 := mk(t, q, "adm2", "Admin", 0)
	ag := mk(t, q, "agx", "Agent", 0)
	ag2 := mk(t, q, "ag2", "Agent", 0)
	sl2 := mk(t, q, "sl2", "Sales", ag2.ID)
	ca := login(t, h, "adm")
	_ = c

	// Admin: cannot create SuperAdmin/Admin, cannot touch SuperAdmin/Admin
	wantCode(t, do(h, "POST", "/admin/users", uform("xxx1", "SuperAdmin"), ca), 403, "admin creates superadmin")
	wantCode(t, do(h, "POST", "/admin/users", uform("xxx2", "Admin"), ca), 403, "admin creates admin")
	wantCode(t, do(h, "POST", "/admin/users", uform("xxx3", "Report"), ca), 303, "admin creates report")
	wantCode(t, do(h, "GET", "/admin/users/1/edit", nil, ca), 404, "admin views superadmin")
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(adm2.ID), uform("adm2", "Report"), ca), 404, "admin edits admin")
	wantCode(t, do(h, "POST", "/admin/users/1/delete", nil, ca), 404, "admin deletes superadmin")
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(ag.ID), uform("agx", "SuperAdmin"), ca), 403, "admin promotes agent to superadmin")
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(ag.ID), uform("agx", "Report"), ca), 303, "admin demotes agent")
	if n := byName(t, q, "agx"); n.Role != "Report" || n.SessionVersion != 1 {
		t.Fatalf("role change must bump session version: %+v", n)
	}
	if _, err := q.GetAdminByUsername(t.Context(), "xxx1"); err == nil {
		t.Fatal("x1 created")
	}

	// Agent: Sales under itself only, whatever the form says
	agb := mk(t, q, "agb", "Agent", 0)
	sl := mk(t, q, "slb", "Sales", agb.ID)
	cg := login(t, h, "agb")
	f := uform("newsales", "SuperAdmin")
	f.Set("root", itoa(ag2.ID))
	wantCode(t, do(h, "POST", "/admin/users", f, cg), 303, "agent creates")
	if n := byName(t, q, "newsales"); n.Role != "Sales" || n.RootID.Int64 != agb.ID {
		t.Fatalf("agent-created: %+v", n)
	}
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(sl.ID), uform("slb", "Agent"), cg), 303, "agent edits own sales")
	if n := byName(t, q, "slb"); n.Role != "Sales" || n.RootID.Int64 != agb.ID {
		t.Fatalf("agent edited: %+v", n)
	}
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(sl2.ID), uform("sl2", "Sales"), cg), 404, "agent edits foreign sales")
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(adm.ID), uform("adm", "Sales"), cg), 404, "agent edits admin")
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(sl.ID)+"/delete", nil, cg), 403, "agent deletes")
	if w := do(h, "GET", "/admin/users", nil, cg); !strings.Contains(w.Body.String(), "slb") || strings.Contains(w.Body.String(), "sl2") ||
		strings.Contains(w.Body.String(), "alice") {
		t.Fatal("agent list scope")
	}
	if w := do(h, "GET", "/admin/users", nil, ca); strings.Contains(w.Body.String(), "alice") || strings.Contains(w.Body.String(), "adm2") ||
		!strings.Contains(w.Body.String(), "rita") {
		t.Fatal("admin list scope")
	}
	// Sales may not reach the user admin at all
	mk(t, q, "sales1", "Sales", agb.ID)
	wantCode(t, do(h, "GET", "/admin/users", nil, login(t, h, "sales1")), 403, "sales")
}

func TestAdminSelfAndLastSuperAdmin(t *testing.T) {
	_, h, q, c := crudApp(t)
	// self: no delete, no role/status change, no password change here
	wantCode(t, do(h, "POST", "/admin/users/1/delete", nil, c), 303, "self delete")
	f := uform("alice", "Report")
	f.Set("status", "Inactive")
	wantCode(t, do(h, "POST", "/admin/users/1", f, c), 303, "self edit")
	a := byName(t, q, "alice")
	if a.Role != "SuperAdmin" || a.Status != "Active" || a.SessionVersion != 0 ||
		bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte("secret123")) != nil {
		t.Fatalf("self edit changed protected fields: %+v", a)
	}

	// alice is the last SuperAdmin: the SQL guard refuses delete, demote and deactivate
	n, err := q.DeleteAdmin(t.Context(), 1)
	if err != nil || n != 0 {
		t.Fatalf("last SuperAdmin deleted: n=%d err=%v", n, err)
	}
	for _, p := range []db.UpdateAdminParams{
		{Username: "alice", Fullname: "alice", Role: "Admin", Status: "Active", ID: 1},
		{Username: "alice", Fullname: "alice", Role: "SuperAdmin", Status: "Inactive", ID: 1},
	} {
		if n, err := q.UpdateAdmin(t.Context(), p); err != nil || n != 0 {
			t.Fatalf("last SuperAdmin changed: %+v n=%d err=%v", p, n, err)
		}
	}
	// the form shows the protection as a validation error
	bob := mk(t, q, "bob", "SuperAdmin", 0)
	cb := login(t, h, "bob")
	wantCode(t, do(h, "POST", "/admin/users/1", uform("alice", "Admin"), cb), 303, "demote one of two")
	// bob is now the last: demoting or deleting through the other SuperAdmin-less path fails
	if n, _ := q.DeleteAdmin(t.Context(), bob.ID); n != 0 {
		t.Fatal("last SuperAdmin deleted after demotion")
	}
	// re-promote alice; with two SuperAdmins one can delete the other
	q.UpdateAdmin(t.Context(), db.UpdateAdminParams{Username: "alice", Fullname: "alice", Role: "SuperAdmin", Status: "Active", ID: 1})
	wantCode(t, do(h, "POST", "/admin/users/1/delete", nil, cb), 303, "delete second superadmin")
	if _, err := q.GetAdmin(t.Context(), 1); err == nil {
		t.Fatal("alice not deleted")
	}
	if _, err := q.GetAdmin(t.Context(), bob.ID); err != nil {
		t.Fatal("bob gone")
	}
}

func TestAdminSessionEndsOnChange(t *testing.T) {
	_, h, q, c := crudApp(t)
	b := mk(t, q, "bob", "Admin", 0)
	cb := login(t, h, "bob")
	wantCode(t, do(h, "GET", "/admin/users", nil, cb), 200, "bob before")
	// own edit without password/role change keeps the session
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(b.ID), uform("bob", "Admin"), cb), 303, "self edit")
	wantCode(t, do(h, "GET", "/admin/users", nil, cb), 200, "bob after self edit")
	// alice sets bob's password: bob's session is dead
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(b.ID), uform("bob", "Admin"), c), 303, "password change")
	if w := do(h, "GET", "/admin/users", nil, cb); w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatalf("old session still valid: %d", w.Code)
	}
	if w := do(h, "POST", "/login", url.Values{"username": {"bob"}, "password": {"secret123"}}, nil); w.Header().Get("Location") == "/admin" {
		t.Fatal("old password accepted")
	}
	// role change ends sessions
	d := mk(t, q, "dee", "Agent", 0)
	cd := login(t, h, "dee")
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(d.ID), uform("dee", "Report"), c), 303, "role change")
	if w := do(h, "GET", "/admin", nil, cd); w.Code != 303 {
		t.Fatal("role-changed admin keeps session")
	}
	// delete ends sessions
	e := mk(t, q, "eve", "Admin", 0)
	ce := login(t, h, "eve")
	wantCode(t, do(h, "POST", "/admin/users/"+itoa(e.ID)+"/delete", nil, c), 303, "delete")
	if w := do(h, "GET", "/admin", nil, ce); w.Code != 303 {
		t.Fatal("deleted admin keeps session")
	}
}

func TestReportRoleForbidden(t *testing.T) {
	_, h, _, _ := crudApp(t)
	cr := login(t, h, "rita")
	for _, p := range [][2]string{{"GET", "/admin/users"}, {"GET", "/admin/users/new"}, {"POST", "/admin/users"},
		{"GET", "/admin/users/1/edit"}, {"POST", "/admin/users/1"}, {"POST", "/admin/users/1/delete"}} {
		wantCode(t, do(h, p[0], p[1], url.Values{}, cr), 403, p[0]+" "+p[1])
	}
	wantCode(t, do(h, "GET", "/admin/password", nil, cr), 200, "report may change own password")
}

func TestChangeOwnPassword(t *testing.T) {
	_, h, q, c := crudApp(t)
	post := func(cur, np, cp string) (int, []*http.Cookie) {
		w := do(h, "POST", "/admin/password", url.Values{"current": {cur}, "password": {np}, "cpassword": {cp}}, c)
		return w.Code, w.Result().Cookies()
	}
	for _, x := range [][3]string{{"wrong", "newpass1", "newpass1"}, {"secret123", "newpass1", "different"}, {"secret123", "123", "123"}} {
		if code, _ := post(x[0], x[1], x[2]); code != 422 {
			t.Fatalf("%v: got %d", x, code)
		}
	}
	if a := byName(t, q, "alice"); bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte("secret123")) != nil || a.SessionVersion != 0 {
		t.Fatal("rejected attempts changed the password")
	}
	code, cookies := post("secret123", "newpass1", "newpass1")
	if code != 303 {
		t.Fatalf("change: %d", code)
	}
	a := byName(t, q, "alice")
	if bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte("newpass1")) != nil || a.SessionVersion != 1 {
		t.Fatalf("not stored: %+v", a)
	}
	var nc *http.Cookie
	for _, ck := range cookies {
		if ck.Name == "gobill_session" {
			nc = ck
		}
	}
	if nc == nil || nc.Value == c.Value {
		t.Fatal("session token not rotated")
	}
	wantCode(t, do(h, "GET", "/admin", nil, nc), 200, "new session works")
	if w := do(h, "GET", "/admin", nil, c); w.Code != 303 {
		t.Fatalf("old token still valid: %d", w.Code)
	}
	logs, _ := q.ListActivityLogs(t.Context(), db.ListActivityLogsParams{Limit: 5})
	if len(logs) != 1 || logs[0].Action != "users.password" {
		t.Fatalf("logs: %+v", logs)
	}
}
