package web

// Gradual migration of a router from local accounts to RADIUS (SuperAdmin only): test a login,
// turn use-radius on, disable the local accounts, or roll back. See internal/billing/migrate.go.
// No step disconnects a session; a running session moves to RADIUS on its next login.

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/radius"
)

// migTested is the session key that says this admin passed step 1 for the router in this session.
func migTested(id int64) string { return fmt.Sprint("mig_tested_", id) }

// migFail turns a migration error into a message on the migration page.
func (s *Server) migFail(w http.ResponseWriter, r *http.Request, back string, err error) {
	t := func(k string) string { return s.catalog.T(s.language(), k) }
	switch {
	case errors.Is(err, billing.ErrRadiusRouter):
		s.sessions.Put(r.Context(), "error", t("Router is in RADIUS mode, nothing to migrate"))
	case errors.Is(err, billing.ErrMigNoAccounts):
		s.sessions.Put(r.Context(), "error", t("The router has no local account of a gobill customer to migrate"))
	case errors.Is(err, billing.ErrMigOrder):
		s.sessions.Put(r.Context(), "error", t("This step is not allowed now. Follow the steps in order"))
	default:
		s.putFailure(r, t("The router step failed, check the router before going on")+". "+t(routerErr(err, msgRouterOther)), err)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// routerMigration is GET /admin/routers/{id}/migration: where the router is, the step 1 test, the
// buttons that are allowed now and the hotspot walled garden block.
func (s *Server) routerMigration(w http.ResponseWriter, r *http.Request) {
	x, err := s.queries.GetRouter(r.Context(), pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := s.language()
	t := func(k string) string { return s.catalog.T(lang, k) }
	if s.Billing == nil {
		s.sessions.Put(r.Context(), "error", t("Router connection is not configured"))
		http.Redirect(w, r, "/admin/routers", http.StatusSeeOther)
		return
	}
	base := fmt.Sprint("/admin/routers/", x.ID, "/migration")
	st := s.Billing.Migration(r.Context(), x.ID)
	where := []string{t("Not started"), t("Step 1 done: login tested"), t("Step 2 done: use-radius is on"), t("Step 3 done: local accounts are disabled")}[st.Step]
	if st.At != 0 {
		where += " (" + s.ts(st.At) + ")"
	}
	sec1, sec2 := t("Step 1: test a login"), t("Steps 2 and 3, rollback")
	out := []field{{Name: "mig_state", Label: t("Where this router is"), Type: "pre", Value: x.Name + ": " + where,
		Hint: "Local accounts are only disabled, never deleted. Running sessions are not cut: they move to RADIUS when they log in again."}}
	if res := s.sessions.PopString(r.Context(), "mig_result"); res != "" {
		out = append(out, field{Name: "mig_result", Label: t("Test result"), Type: "pre", Value: res})
	}
	_, accts, err := s.Billing.MigrationAccounts(r.Context(), x.ID)
	var opts []option
	for _, a := range accts {
		opts = append(opts, option{a.Type + ":" + a.Name, a.Type + ": " + a.Name})
	}
	form := base + "/test"
	switch {
	case err == nil:
		out = append(out, field{Name: "account", Label: "Account to test", Type: "select", Value: opts[0].Value, Options: opts, Section: sec1,
			Hint: "gobill checks the router's own password of this account through its RADIUS login. The router is not changed. Press Save to run the test."})
	case errors.Is(err, billing.ErrRadiusRouter):
		out = append(out, field{Name: "mig_none", Label: t("Router is in RADIUS mode, nothing to migrate"), Type: "note", Section: sec1})
	case errors.Is(err, billing.ErrMigNoAccounts):
		out = append(out, field{Name: "mig_none", Label: t("The router has no local account of a gobill customer to migrate"), Type: "note", Section: sec1})
	default:
		out = append(out, field{Name: "mig_none", Label: t("Could not read the router, nothing was changed") + ": " + t(routerErr(err, msgRouterOther)), Type: "note", Section: sec1})
	}
	btn := func(name, label, act, confirm string) field {
		return field{Name: name, Type: "formbtn", Section: sec2, Value: base + "/" + act, Btn: label, Confirm: confirm}
	}
	tested := s.sessions.GetBool(r.Context(), migTested(x.ID))
	if st.Step == 1 && tested {
		out = append(out, btn("mig_switch", "Step 2: turn on use-radius", "switch", ""))
	}
	if st.Step == 2 {
		out = append(out, btn("mig_disable", "Step 3: disable local accounts", "disable", ""))
	}
	if st.Step >= 1 {
		out = append(out, btn("mig_rollback", "Roll back", "rollback", "Turn use-radius off and enable the local accounts again? Nothing is deleted."))
	}
	if sets, err := s.loadSettings(r.Context()); err == nil {
		host, port := s.gobillAddr()
		var b strings.Builder
		fmt.Fprintf(&b, "/ip hotspot walled-garden ip add dst-address=%s dst-port=%s action=accept", host, port)
		for _, h := range isolir.HostLines(isolir.Value(sets, "isolir_allow_hosts")) {
			fmt.Fprintf(&b, "\n/ip hotspot walled-garden add dst-host=%s action=allow", h)
		}
		out = append(out, field{Name: "mig_walled", Label: "Hotspot walled garden for the payment and isolir pages", Type: "pre", Section: t("Hotspot only"), Value: b.String(),
			Hint: "Paste this on the router yourself: gobill does not apply it. The payment page and the isolir page are served by gobill at the first line. If customers pay with a payment gateway, also allow the gateway hosts."})
	}
	s.renderForm(w, r, 200, formPage{"Migrate router to RADIUS", form, "/admin/routers", out})
}

// routerMigrationAct is POST /admin/routers/{id}/migration/{act} for test, switch, disable and rollback.
// switch and disable first show what will happen and apply only when that page was confirmed.
func (s *Server) routerMigrationAct(w http.ResponseWriter, r *http.Request) {
	x, err := s.queries.GetRouter(r.Context(), pathID(r))
	if err != nil || s.Billing == nil {
		http.NotFound(w, r)
		return
	}
	ctx, admin := r.Context(), adminFrom(r)
	t := func(k string) string { return s.catalog.T(s.language(), k) }
	back := fmt.Sprint("/admin/routers/", x.ID, "/migration")
	act := r.PathValue("act")
	confirm := r.PostFormValue("confirm") == "1"
	preview := func(lines ...field) {
		s.renderForm(w, r, 200, formPage{"Migrate router to RADIUS", back + "/" + act, back,
			append(lines, field{Name: "confirm", Type: "hidden", Value: "1"}, field{Name: "confirm_note", Type: "note", Label: "Press Save to apply this. Nothing is deleted and running sessions are not cut."})})
	}
	switch act {
	case "test":
		key := r.PostFormValue("account")
		var d radius.Decision
		rs := s.radiusServer()
		reject, err := s.Billing.MigrationTest(ctx, x.ID, key, func(user, pw string) string {
			if pw == "" || pw == user { // a voucher-style login would redeem a voucher
				return t("This account uses its username as password (voucher style), test another one")
			}
			d = rs.Authorize(ctx, radius.AuthRequest{User: user, DryRun: true,
				Check: func(stored []byte) (bool, string) { return subtle.ConstantTimeCompare([]byte(pw), stored) == 1, "" }})
			return d.Reject
		})
		if err != nil {
			s.migFail(w, r, back, err)
			return
		}
		s.sessions.Remove(ctx, migTested(x.ID))
		res := t("Login test failed") + ": " + reject
		if reject == "" {
			s.sessions.Put(ctx, migTested(x.ID), true)
			res = fmt.Sprintf("%s: %s, %s %s, %s %s", t("Login test passed"), key, t("plan"), d.Plan.PlanName, t("rate limit"), d.Rate)
		}
		s.logActivity(r, "router.radius_test", x.Name+" "+key)
		s.sessions.Put(ctx, "mig_result", res)
	case "switch":
		if !s.sessions.GetBool(ctx, migTested(x.ID)) || s.Billing.Migration(ctx, x.ID).Step != 1 {
			s.migFail(w, r, back, billing.ErrMigOrder)
			return
		}
		if !confirm {
			_, accts, err := s.Billing.MigrationAccounts(ctx, x.ID)
			if err != nil {
				s.migFail(w, r, back, err)
				return
			}
			preview(field{Name: "mig_cmd", Label: "RouterOS change that will be applied", Type: "pre", Value: billing.MigrationCommands(billing.MigKinds(accts), true)})
			return
		}
		if err := s.Billing.MigrationSwitch(ctx, x.ID, admin.ID, true); err != nil {
			s.migFail(w, r, back, err)
			return
		}
		s.done(w, r, back, "use-radius is on. Running sessions were not cut", "router.radius_switch", x.Name)
		return
	case "disable":
		if s.Billing.Migration(ctx, x.ID).Step != 2 {
			s.migFail(w, r, back, billing.ErrMigOrder)
			return
		}
		if !confirm {
			_, accts, err := s.Billing.MigrationAccounts(ctx, x.ID)
			if err != nil {
				s.migFail(w, r, back, err)
				return
			}
			n := 0
			for _, a := range accts {
				if !a.Disabled {
					n++
				}
			}
			preview(field{Name: "mig_count", Label: fmt.Sprintf("%s %d (%s)", t("Local accounts that will be disabled"), n, t("secrets and hotspot users of gobill customers; others on the router are left as they are")), Type: "note"},
				field{Name: "mig_cmd", Label: "RouterOS change for each account (disabled, not removed)", Type: "pre", Value: "/ppp secret set <name> disabled=yes\n/ip hotspot user set <name> disabled=yes"})
			return
		}
		n, err := s.Billing.MigrationDisable(ctx, x.ID, admin.ID)
		if err != nil {
			s.sessions.Put(ctx, "flash", fmt.Sprintf("%s: %d", t("Local accounts disabled before the failure"), n))
			s.migFail(w, r, back, err)
			return
		}
		s.logActivity(r, "router.radius_disable_local", x.Name)
		s.sessions.Put(ctx, "flash", fmt.Sprintf("%s: %d", t("Local accounts disabled"), n))
	case "rollback":
		n, err := s.Billing.MigrationRollback(ctx, x.ID, admin.ID)
		if err != nil {
			s.migFail(w, r, back, err)
			return
		}
		s.sessions.Remove(ctx, migTested(x.ID))
		s.logActivity(r, "router.radius_rollback", x.Name)
		s.sessions.Put(ctx, "flash", fmt.Sprintf("%s: %d", t("Rolled back. Local accounts enabled"), n))
	default:
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
