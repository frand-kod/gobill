package web

// Login-as-customer impersonation and its end.

import (
	"net/http"

	"fmt"
)

// custLoginAs opens a portal session for the customer in this browser (old customers/login). The
// admin keys of the session are not touched; "impersonator" marks it and drives the portal banner.
func (s *Server) custLoginAs(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	if c.Status == "Banned" || c.Status == "Disabled" {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Customer not found"))
		http.Redirect(w, r, fmt.Sprint("/admin/customers/", c.ID), http.StatusSeeOther)
		return
	}
	s.sessions.Put(r.Context(), "customer_id", c.ID)
	s.sessions.Put(r.Context(), "csv", c.SessionVersion)
	s.sessions.Put(r.Context(), "impersonator", adminFrom(r).ID)
	s.logActivity(r, "customer.impersonate", c.Username)
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// pImpersonateEnd ends only the customer session and returns to that customer's admin page.
func (s *Server) pImpersonateEnd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	to, cid := "/portal/login", s.sessions.GetInt64(ctx, "customer_id")
	if s.sessions.GetInt64(ctx, "impersonator") != 0 {
		to = "/admin"
		if cid != 0 {
			to = fmt.Sprint("/admin/customers/", cid)
		}
	}
	for _, k := range []string{"customer_id", "csv", "impersonator"} {
		s.sessions.Remove(ctx, k)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
