package web

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

// Role rules follow the old settings.php users-* cases, but stricter: the old code let an
// Admin hand out SuperAdmin and an Agent pick any role; here a role must be in assignable.

// assignable lists the roles the actor may give to other admins.
func assignable(a *db.Admin) []string {
	switch a.Role {
	case "SuperAdmin":
		return []string{"SuperAdmin", "Admin", "Report", "Agent", "Sales"}
	case "Admin":
		return []string{"Report", "Agent", "Sales"}
	case "Agent":
		return []string{"Sales"}
	}
	return nil
}

// canManage: SuperAdmin manages all; Admin the lower roles; Agent only its own Sales. Everyone manages self.
func canManage(a *db.Admin, t db.Admin) bool {
	if a.ID == t.ID {
		return true
	}
	switch a.Role {
	case "SuperAdmin":
		return true
	case "Admin":
		return t.Role != "SuperAdmin" && t.Role != "Admin"
	case "Agent":
		return t.Role == "Sales" && t.RootID.Valid && t.RootID.Int64 == a.ID
	}
	return false
}

// adminScope is the SearchAdmins scope: what the actor may see.
func adminScope(a *db.Admin) string {
	switch a.Role {
	case "SuperAdmin":
		return "all"
	case "Admin":
		return "admin"
	}
	return "agent"
}

func roleOptions(roles []string) []option {
	var o []option
	for _, r := range roles {
		o = append(o, option{r, r})
	}
	return o
}

// adminFields builds the form; a nil target is "create". Self cannot change role, status or password here.
func (s *Server) adminFields(r *http.Request, target *db.Admin, v, e map[string]string) []field {
	actor := adminFrom(r)
	self := target != nil && target.ID == actor.ID
	out := section([]field{
		text("username", "Username", v, e).req(),
		text("fullname", "Full Name", v, e).req(),
		text("email", "Email", v, e).as("email"),
		text("phone", "Phone Number", v, e),
		text("city", "City", v, e),
	}, "Admin", "")
	var acc []field
	if !self {
		pw := text("password", "Password", v, e).as("password")
		pw.Value = "" // never rendered back
		cp := text("cpassword", "Confirm Password", v, e).as("password")
		cp.Value = ""
		if target == nil {
			pw.Required = true
		} else {
			pw.Hint = "Leave empty to keep the current password"
		}
		acc = append(acc, pw, cp)
		if actor.Role != "Agent" {
			acc = append(acc, field{Name: "role", Label: "User Type", Type: "select", Value: v["role"], Error: e["role"],
				Options: roleOptions(assignable(actor)), Bind: true})
			agents, _ := s.queries.ListAgents(r.Context())
			ro := []option{{"", "-"}}
			for _, a := range agents {
				ro = append(ro, option{strconv.FormatInt(a.ID, 10), a.Username})
			}
			acc = append(acc, field{Name: "root", Label: "Agent", Type: "select", Value: v["root"], Error: e["root"],
				Options: ro, Show: "role=='Sales'", Hint: "Agent that owns this Sales user"})
		}
		if target != nil {
			acc = append(acc, field{Name: "status", Label: "Status", Type: "select", Value: v["status"],
				Options: []option{{"Active", "Active"}, {"Inactive", "Inactive"}}})
		}
	}
	return append(out, section(acc, "Account", "")...)
}

func (s *Server) adminList(w http.ResponseWriter, r *http.Request) {
	actor := adminFrom(r)
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchAdmins(r.Context(), db.SearchAdminsParams{
		Q: sql.NullString{String: q, Valid: true}, Scope: adminScope(actor), Actor: actor.ID, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list admins", err)
		return
	}
	lp := listPage{Heading: "Admin Users", Base: "/admin/users", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		NoDelete: actor.Role == "Agent", Cols: []string{"Username", "Full Name", "User Type", "Status", "Last Login"}}
	for _, a := range rows {
		last := "-"
		if a.LastLoginAt.Valid {
			last = s.ts(a.LastLoginAt.Int64)
		}
		lp.Rows = append(lp.Rows, listRow{a.ID, []string{a.Username, a.Fullname, a.Role, a.Status, last}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) adminNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"role": assignable(adminFrom(r))[0]}
	s.renderForm(w, r, 200, formPage{"Add User", "/admin/users", "/admin/users", s.adminFields(r, nil, v, nil)})
}

// loadManaged loads the target of {id}; 404 unless the actor may manage it.
func (s *Server) loadManaged(w http.ResponseWriter, r *http.Request) (db.Admin, bool) {
	t, err := s.queries.GetAdmin(r.Context(), pathID(r))
	if err == sql.ErrNoRows || (err == nil && !canManage(adminFrom(r), t)) {
		http.NotFound(w, r)
		return t, false
	} else if err != nil {
		s.fail(w, "get admin", err)
		return t, false
	}
	return t, true
}

func (s *Server) adminEdit(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadManaged(w, r)
	if !ok {
		return
	}
	v := map[string]string{"username": t.Username, "fullname": t.Fullname, "email": t.Email, "phone": t.Phone,
		"city": t.City, "role": t.Role, "status": t.Status}
	if t.RootID.Valid {
		v["root"] = strconv.FormatInt(t.RootID.Int64, 10)
	}
	s.renderForm(w, r, 200, formPage{"Edit User", fmt.Sprint("/admin/users/", t.ID), "/admin/users", s.adminFields(r, &t, v, nil)})
}

func (s *Server) adminSave(w http.ResponseWriter, r *http.Request) {
	actor := adminFrom(r)
	id := pathID(r)
	var t *db.Admin
	if id != 0 {
		cur, ok := s.loadManaged(w, r)
		if !ok {
			return
		}
		t = &cur
	}
	self := t != nil && t.ID == actor.ID
	v := formVals(r, "username", "fullname", "email", "phone", "city", "role", "status", "root")
	pass, cpass := r.PostFormValue("password"), r.PostFormValue("cpassword")
	e := map[string]string{}

	// Role and status: the server decides, never the form, for self and for Agents.
	role, status := v["role"], v["status"]
	switch {
	case self:
		role, status = t.Role, t.Status
		pass = "" // own password goes through /admin/password
	case actor.Role == "Agent":
		role = "Sales"
	case !oneOf(role, assignable(actor)...):
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if t == nil {
		status = "Active"
	} else if !oneOf(status, "Active", "Inactive") {
		status = t.Status
	}
	v["role"], v["status"] = role, status

	if n := len([]rune(v["username"])); n < 3 || n > 45 {
		e["username"] = "Username should be between 3 to 45 characters"
	}
	if n := len([]rune(v["fullname"])); n < 3 || n > 45 {
		e["fullname"] = "Full Name should be between 3 to 45 characters"
	}
	if v["email"] != "" {
		if a, err := mail.ParseAddress(v["email"]); err != nil || a.Address != v["email"] {
			e["email"] = "Enter a valid email address"
		}
	}
	if pass != "" || t == nil {
		if n := len(pass); n < 6 || n > 72 { // bcrypt reads at most 72 bytes
			e["password"] = "Password should be 6 to 72 characters"
		} else if pass != cpass {
			e["cpassword"] = "Passwords does not match"
		}
	}
	root := sql.NullInt64{}
	if role == "Sales" {
		if actor.Role == "Agent" {
			root = sql.NullInt64{Int64: actor.ID, Valid: true}
		} else if v["root"] != "" {
			rid, _ := strconv.ParseInt(v["root"], 10, 64)
			if ag, err := s.queries.GetAdmin(r.Context(), rid); err != nil || ag.Role != "Agent" {
				e["root"] = "Agent not found"
			} else {
				root = sql.NullInt64{Int64: rid, Valid: true}
			}
		}
	}
	again := func(status int) {
		heading, action := "Add User", "/admin/users"
		if t != nil {
			heading, action = "Edit User", fmt.Sprint("/admin/users/", t.ID)
		}
		s.renderForm(w, r, status, formPage{heading, action, "/admin/users", s.adminFields(r, t, v, e)})
	}
	if len(e) > 0 {
		again(422)
		return
	}
	var hash []byte
	if pass != "" {
		var err error
		if hash, err = bcrypt.GenerateFromPassword([]byte(pass), bcryptCost); err != nil {
			s.fail(w, "hash password", err)
			return
		}
	}

	if t == nil {
		_, err := s.queries.CreateAdmin(r.Context(), db.CreateAdminParams{Username: v["username"], Fullname: v["fullname"],
			PasswordHash: string(hash), Role: role, Email: v["email"], Phone: v["phone"], City: v["city"], RootID: root})
		if isUnique(err) {
			e["username"] = "Account already exist"
			again(422)
			return
		} else if err != nil {
			s.fail(w, "create admin", err)
			return
		}
		s.done(w, r, "/admin/users", "Account Created Successfully", "users.create", v["username"]+" ("+role+")")
		return
	}

	// Any change that must cut off existing sessions bumps session_version.
	var bump int64
	if pass != "" || role != t.Role || status != t.Status {
		bump = 1
	}
	n, err := s.queries.UpdateAdmin(r.Context(), db.UpdateAdminParams{Username: v["username"], Fullname: v["fullname"],
		Email: v["email"], Phone: v["phone"], City: v["city"], Role: role, Status: status, RootID: root,
		PasswordHash: string(hash), Bump: bump, ID: t.ID})
	switch {
	case isUnique(err):
		e["username"] = "Account already exist"
		again(422)
		return
	case err != nil:
		s.fail(w, "update admin", err)
		return
	case n == 0:
		e["role"] = "The last active SuperAdmin cannot be demoted or deactivated"
		again(422)
		return
	}
	desc := v["username"]
	if role != t.Role {
		desc += ": " + t.Role + " -> " + role
	}
	if pass != "" {
		desc += ": password changed"
	}
	s.done(w, r, "/admin/users", "User Updated Successfully", "users.update", desc)
}

func (s *Server) adminDelete(w http.ResponseWriter, r *http.Request) {
	actor := adminFrom(r)
	t, ok := s.loadManaged(w, r)
	if !ok {
		return
	}
	if t.ID == actor.ID {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Sorry You can't delete yourself"))
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	n, err := s.queries.DeleteAdmin(r.Context(), t.ID)
	if err != nil {
		s.fail(w, "delete admin", err)
		return
	}
	if n == 0 {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "The last active SuperAdmin cannot be deleted"))
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	s.done(w, r, "/admin/users", "User deleted Successfully", "users.delete", t.Username+" ("+t.Role+")")
}

// ---- change own password ----

func pwFields(e map[string]string) []field {
	return section([]field{
		text("current", "Current Password", nil, e).as("password").req(),
		text("password", "New Password", nil, e).as("password").req().hint("6 to 72 characters"),
		text("cpassword", "Confirm New Password", nil, e).as("password").req(),
	}, "", "")
}

func (s *Server) passwordForm(w http.ResponseWriter, r *http.Request) {
	s.renderForm(w, r, 200, formPage{"Change Password", "/admin/password", "/admin", pwFields(nil)})
}

func (s *Server) passwordSave(w http.ResponseWriter, r *http.Request) {
	actor := adminFrom(r)
	cur, pass, cpass := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("cpassword")
	e := map[string]string{}
	again := func(status int) {
		s.renderForm(w, r, status, formPage{"Change Password", "/admin/password", "/admin", pwFields(e)})
	}
	// The current password is a guess target for a hijacked session: share the login throttle.
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		e["current"] = "Too many failed attempts. Try again in 15 minutes."
		again(http.StatusTooManyRequests)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(actor.PasswordHash), []byte(cur)) != nil {
		s.recordFailure(ip)
		e["current"] = "Incorrect Current Password"
		again(422)
		return
	}
	if n := len(pass); n < 6 || n > 72 {
		e["password"] = "Password should be 6 to 72 characters"
	} else if pass != cpass {
		e["cpassword"] = "Passwords does not match"
	}
	if len(e) > 0 {
		again(422)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcryptCost)
	if err != nil {
		s.fail(w, "hash password", err)
		return
	}
	sv, err := s.queries.SetAdminPassword(r.Context(), db.SetAdminPasswordParams{PasswordHash: string(hash), ID: actor.ID})
	if err != nil {
		s.fail(w, "set password", err)
		return
	}
	// Other sessions of this admin die with the bumped version; this one gets a new token.
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		slog.Error("renew session", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.sessions.Put(r.Context(), "sv", sv)
	s.done(w, r, "/admin", "Password changed successfully", "users.password", actor.Username)
}
