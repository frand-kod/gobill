package web

// Admin account management and own-password change.

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/service/admin"
	"golang.org/x/crypto/bcrypt"
	"strconv"
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
		pw.Hint = "10 to 72 characters"
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
				Options: ro, Show: "role=='Sales'"})
		}
		if target != nil {
			acc = append(acc, field{Name: "status", Label: "Status", Type: "select", Value: v["status"],
				Options: []option{{"Active", "Active"}, {"Inactive", "Inactive"}}})
			if actor.Role == "SuperAdmin" && !self {
				acc = append(acc, field{Name: "resetpw", Label: "Password", Type: "formbtn", Hint: "Sets a new random password, shown once. The admin is signed out everywhere.",
					Value: "/admin/users/" + strconv.FormatInt(target.ID, 10) + "/password/reset", Btn: "Reset password",
					Confirm: "Reset this admin's password? They will be signed out everywhere."})
			}
			if actor.Role == "SuperAdmin" && !self && target.TotpEnabled == 1 {
				acc = append(acc, field{Name: "reset2fa", Label: "Two-factor login", Type: "formbtn", Hint: "Use when the admin lost the authenticator and recovery codes.",
					Value: "/admin/users/" + strconv.FormatInt(target.ID, 10) + "/2fa/reset", Btn: "Reset 2FA",
					Confirm: "Reset this admin's two-factor login? They can sign in with the password alone until they set it up again."})
			}
		}
	}
	return append(out, section(acc, "Account", "")...)
}

func (s *Server) adminList(w http.ResponseWriter, r *http.Request) {
	actor := adminFrom(r)
	q, pg := paging(r)
	rows, err := s.queries.SearchAdmins(r.Context(), db.SearchAdminsParams{
		Q: sql.NullString{String: q, Valid: true}, Scope: adminScope(actor), Actor: actor.ID, PageLimit: pg.limit(), PageOffset: pg.offset()})
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
	total, err := s.queries.CountFilteredAdmins(r.Context(), db.CountFilteredAdminsParams{Q: sql.NullString{String: q, Valid: true}, Scope: adminScope(actor), Actor: actor.ID})
	if err != nil {
		s.fail(w, "count admins", err)
		return
	}
	lp.finish(pg, total)
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
	v := formVals(r, "username", "fullname", "email", "phone", "city", "role", "status", "root")
	res, err := s.admins().Save(r.Context(), admin.SaveInput{ActorID: actor.ID, ActorRole: actor.Role, Assignable: assignable(actor),
		Target: t, V: v, Pass: r.PostFormValue("password"), CPass: r.PostFormValue("cpassword")})
	if errors.Is(err, admin.ErrForbidden) {
		s.deny(w, r, http.StatusForbidden)
		return
	} else if err != nil {
		s.fail(w, "save admin", err)
		return
	}
	v["role"], v["status"] = res.Role, res.Status
	if len(res.Errs) > 0 {
		heading, action := "Add User", "/admin/users"
		if t != nil {
			heading, action = "Edit User", fmt.Sprint("/admin/users/", t.ID)
		}
		s.renderForm(w, r, 422, formPage{heading, action, "/admin/users", s.adminFields(r, t, v, res.Errs)})
		return
	}
	if t == nil {
		s.done(w, r, "/admin/users", "Account Created Successfully", "users.create", res.Desc)
		return
	}
	s.done(w, r, "/admin/users", "User Updated Successfully", "users.update", res.Desc)
}

func (s *Server) admins() *admin.Service {
	return &admin.Service{Q: s.queries, Cost: bcryptCost}
}

func (s *Server) adminDelete(w http.ResponseWriter, r *http.Request) {
	actor := adminFrom(r)
	t, ok := s.loadManaged(w, r)
	if !ok {
		return
	}
	if t.ID == actor.ID {
		s.sessions.Put(r.Context(), "error", s.t("Sorry You can't delete yourself"))
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	n, err := s.queries.DeleteAdmin(r.Context(), t.ID)
	if err != nil {
		s.fail(w, "delete admin", err)
		return
	}
	if n == 0 {
		s.sessions.Put(r.Context(), "error", s.t("The last active SuperAdmin cannot be deleted"))
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	s.done(w, r, "/admin/users", "User deleted Successfully", "users.delete", t.Username+" ("+t.Role+")")
}

// ---- change own password ----

func pwFields(e map[string]string, forced bool) []field {
	cur := text("current", "Current Password", nil, e).as("password").req()
	cur.Cur = true
	var top []field
	if forced { // imported sha1 hash: auth.go sends every page here until the password is changed
		top = []field{{Name: "legacy", Type: "alert", Label: "Your password is from the old system and is stored in a weak format. Choose a new one to continue. You cannot open other pages until you do."}}
	}
	return append(section(append(top, cur,
		text("password", "New Password", nil, e).as("password").req().hint("10 to 72 characters"),
		text("cpassword", "Confirm New Password", nil, e).as("password").req()), "", ""), section([]field{
		{Name: "2fa", Label: "Two-factor login", Type: "link", Value: "/admin/2fa", Btn: "Manage 2FA"},
	}, "Two-factor login", "")...)
}

// pwPage is the change-password form; while the password is the imported sha1 one, Cancel (which loops back here) is Logout.
func (s *Server) pwPage(a *db.Admin, e map[string]string) formPage {
	forced := a.LegacySha1 != ""
	cancel := "/admin"
	if forced {
		cancel = "/logout"
	}
	return formPage{"Change Password", "/admin/password", cancel, pwFields(e, forced)}
}

func (s *Server) passwordForm(w http.ResponseWriter, r *http.Request) {
	s.renderForm(w, r, 200, s.pwPage(adminFrom(r), nil))
}

func (s *Server) passwordSave(w http.ResponseWriter, r *http.Request) {
	actor := adminFrom(r)
	cur, pass, cpass := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("cpassword")
	e := map[string]string{}
	again := func(status int) {
		s.renderForm(w, r, status, s.pwPage(actor, e))
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
	if n := len(pass); n < 10 || n > 72 {
		e["password"] = "Password should be 10 to 72 characters"
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
		s.errorPage(w, "-")
		return
	}
	s.sessions.Put(r.Context(), "sv", sv)
	s.done(w, r, "/admin", "Password changed successfully", "users.password", actor.Username)
}

// ---- SuperAdmin resets another admin's password ----

// adminPasswordReset sets a random password for another admin and shows it once. The admin's other
// sessions end with the bumped session_version, so they must log in again. The activity log names the
// admin, never the password.
func (s *Server) adminPasswordReset(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadManaged(w, r)
	if !ok {
		return
	}
	back := "/admin/users/" + strconv.FormatInt(t.ID, 10) + "/edit"
	if t.ID == adminFrom(r).ID {
		s.flashTo(w, r, back, "Change your own password under Password")
		return
	}
	pw := secret.RandomPassword(16)
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	if err != nil {
		s.fail(w, "hash password", err)
		return
	}
	if _, err := s.queries.SetAdminPassword(r.Context(), db.SetAdminPasswordParams{PasswordHash: string(hash), ID: t.ID}); err != nil {
		s.fail(w, "reset admin password", err)
		return
	}
	s.logActivity(r, "users.password.reset", t.Username)
	s.render(w, r, 200, "admin_password", Page{Title: "Reset password", Data: adminPasswordPage{Username: t.Username, Password: pw, Back: back}})
}

// adminPasswordPage shows the new password once.
type adminPasswordPage struct{ Username, Password, Back string }
