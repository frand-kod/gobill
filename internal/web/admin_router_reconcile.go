package web

// Reconcile screen of an API-mode router: read it, compare with gobill, apply what the admin confirms.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frand-kod/gobill/internal/billing"
	"golang.org/x/crypto/bcrypt"
)

// reconChoices are the actions a row of each kind offers; the first is the default.
func reconChoices(kind string) []option {
	switch kind {
	case billing.ReconDiffer:
		return []option{{billing.ActPush, "Follow gobill"}, {billing.ActRouter, "Follow router (password only)"}, {billing.ActSkip, "Skip"}}
	case billing.ReconGobill:
		return []option{{billing.ActPush, "Push to router"}, {billing.ActSkip, "Skip"}}
	}
	return []option{{billing.ActSkip, "Leave on router"}, {billing.ActImport, "Import into gobill"}}
}

// routerReconcile shows the comparison (GET); on POST it checks the choices, shows the count summary
// first and applies only when the summary was confirmed. The read happens on each request, the router
// password never reaches the page. A RADIUS or unreachable router stops here with a message.
func (s *Server) routerReconcile(w http.ResponseWriter, r *http.Request) {
	x, err := s.queries.GetRouter(r.Context(), pathID(r))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get router", err)
		return
	}
	lang := s.language()
	t := func(k string) string { return s.catalog.T(lang, k) }
	back := func() { http.Redirect(w, r, "/admin/routers", http.StatusSeeOther) }
	if s.Billing == nil {
		s.sessions.Put(r.Context(), "error", t("Router connection is not configured"))
		back()
		return
	}
	rows, err := s.Billing.Reconcile(r.Context(), x.ID)
	if errors.Is(err, billing.ErrRadiusRouter) {
		s.sessions.Put(r.Context(), "error", t("Router is in RADIUS mode, nothing to import")+": "+x.Name)
		back()
		return
	} else if err != nil {
		s.putFailure(r, t("Could not read the router, nothing was changed")+": "+x.Name+". "+t(routerErr(err, msgRouterOther)), err)
		back()
		return
	}
	post := r.Method == http.MethodPost
	choose := func(row billing.ReconRow) string {
		if v := r.PostFormValue("a:" + row.Key()); post && row.Kind != billing.ReconSame {
			for _, o := range reconChoices(row.Kind) {
				if o.Value == v {
					return v
				}
			}
		}
		return row.DefaultAction()
	}
	base := fmt.Sprint("/admin/routers/", x.ID, "/reconcile")
	if post && r.PostFormValue("confirm") == "1" {
		hash := func(pw string) (string, error) {
			b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
			return string(b), err
		}
		res, err := s.Billing.ReconApply(r.Context(), x.ID, adminFrom(r).ID, choose, hash)
		if err != nil {
			s.fail(w, "reconcile router", err)
			return
		}
		s.logActivity(r, "router.reconcile", x.Name)
		s.sessions.Put(r.Context(), "flash", fmt.Sprintf("%s: %s %d, %s %d, %s %d, %s %d", x.Name, t("Pushed"), res.Pushed, t("Imported"), res.Imported,
			t("Followed router"), res.Followed, t("Skipped"), res.Skipped))
		if n := len(res.Failures); n > 0 {
			s.sessions.Put(r.Context(), "error", fmt.Sprintf("%s: %d. %s", t("Failed"), n, strings.Join(res.Failures[:min(n, 5)], "; ")))
		}
		back()
		return
	}
	count := map[string]int{}
	var fields []field
	for _, row := range rows {
		a := choose(row)
		count[a]++
		if row.Kind == billing.ReconSame {
			count["same"]++
			continue
		}
		label := fmt.Sprintf("%s (%s) %s", row.Name, row.Type, row.Detail)
		if !post { // choose; the summary page below carries the choices as hidden fields
			fields = append(fields, field{Name: "a:" + row.Key(), Label: label, Type: "select", Value: a, Options: reconChoices(row.Kind)})
		} else {
			fields = append(fields, field{Name: "a:" + row.Key(), Type: "hidden", Value: a})
		}
	}
	summary := fmt.Sprintf("%s %d, %s %d, %s %d, %s %d, %s %d", t("Push to router"), count[billing.ActPush], t("Follow router"), count[billing.ActRouter],
		t("Import into gobill"), count[billing.ActImport], t("Skip"), count[billing.ActSkip], t("Unchanged"), count["same"])
	head := []field{{Name: "summary", Type: "note", Label: x.Name + ": " + summary}}
	if post {
		head = append(head, field{Name: "confirm", Type: "hidden", Value: "1"},
			field{Name: "confirm_note", Type: "note", Label: "Press Save to apply these choices. The router is read again first."})
	} else if len(fields) == 0 {
		head = append(head, field{Name: "empty", Type: "note", Label: "Nothing to reconcile: the router and gobill agree."})
	}
	cancel := base // the summary page goes back to the choices
	if !post {
		cancel = "/admin/routers"
	}
	s.renderForm(w, r, 200, formPage{"Reconcile router", base, cancel, append(head, fields...)})
}
