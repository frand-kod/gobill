package customers

// Login-as-customer impersonation and its end.

import (
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"net/http"

	"fmt"
)

// custLoginAs opens a portal session for the customer in this browser (old customers/login). The
// admin keys of the session are not touched; "impersonator" marks it and drives the portal banner.
func (s *Handlers) custLoginAs(w http.ResponseWriter, r *http.Request) {
	c, ok := s.CustGet(w, r)
	if !ok {
		return
	}
	if c.Status == "Banned" || c.Status == "Disabled" {
		s.Sessions.Put(r.Context(), "error", s.T("Customer not found"))
		http.Redirect(w, r, fmt.Sprint("/admin/customers/", c.ID), http.StatusSeeOther)
		return
	}
	s.Sessions.Put(r.Context(), "customer_id", c.ID)
	s.Sessions.Put(r.Context(), "csv", c.SessionVersion)
	s.Sessions.Put(r.Context(), "impersonator", adminx.AdminFrom(r).ID)
	s.LogActivity(r, "customer.impersonate", c.Username)
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// pImpersonateEnd ends only the customer session and returns to that customer's admin page.
func (s *Handlers) pImpersonateEnd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	to, cid := "/login", s.Sessions.GetInt64(ctx, "customer_id")
	if s.Sessions.GetInt64(ctx, "impersonator") != 0 {
		to = "/admin"
		if cid != 0 {
			to = fmt.Sprint("/admin/customers/", cid)
		}
	}
	for _, k := range []string{"customer_id", "csv", "impersonator"} {
		s.Sessions.Remove(ctx, k)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
