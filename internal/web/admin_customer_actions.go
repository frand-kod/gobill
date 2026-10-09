package web

// Customer actions: deactivate, sync and welcome message.

import (
	"log/slog"
	"net/http"

	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
)

// ---- customer detail actions ----

// custAct runs a billing action for the customer and redirects back to the detail page.
func (s *Server) custAct(w http.ResponseWriter, r *http.Request, action, okMsg string, do func(ctx context.Context, id, adminID int64) (int, error)) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	if s.Billing == nil {
		s.fail(w, action, errors.New("billing service not configured"))
		return
	}
	back := fmt.Sprint("/admin/customers/", c.ID)
	n, err := do(r.Context(), c.ID, adminFrom(r).ID)
	switch {
	case err != nil:
		slog.Error(action, "customer", c.Username, "err", err)
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Action failed"))
		http.Redirect(w, r, back, http.StatusSeeOther)
	case n == 0:
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Cannot find active plan"))
		http.Redirect(w, r, back, http.StatusSeeOther)
	default:
		s.done(w, r, back, okMsg, action, c.Username)
	}
}

func (s *Server) custDeactivate(w http.ResponseWriter, r *http.Request) {
	s.custAct(w, r, "customer.deactivate", "Success deactivate customer to Mikrotik", s.billingDeactivate)
}

func (s *Server) custSync(w http.ResponseWriter, r *http.Request) {
	s.custAct(w, r, "customer.sync", "Success sync customer to router", s.billingSync)
}

func (s *Server) billingDeactivate(ctx context.Context, id, adminID int64) (int, error) {
	return s.Billing.DeactivateCustomer(ctx, id, adminID)
}

func (s *Server) billingSync(ctx context.Context, id, adminID int64) (int, error) {
	return s.Billing.SyncCustomer(ctx, id, adminID)
}

// ---- welcome message ----

func welcomeChannels(v map[string]string) (out []string) {
	for _, k := range []string{"sms", "wa", "email"} {
		if v["notify_"+k] == "1" {
			out = append(out, k)
		}
	}
	return
}

// welcome sends welcome_message on the chosen channels, in the background. The password is never
// part of the message: the template's [[Password]] placeholder is filled with asterisks.
func (s *Server) welcome(ctx context.Context, c db.Customer, vias []string) {
	if len(vias) == 0 {
		return
	}
	st, err := s.loadSettings(ctx)
	if err == nil {
		var n *notify.Notifier
		if n, err = notify.Load(ctx, s.queries); err == nil {
			n.Log = notify.LogTo(s.queries)
			n.Go("welcome", func(ctx context.Context) error {
				return n.CustomOn(ctx, c, vias, "welcome_message", "Welcome", map[string]string{"company": st["company_name"],
					"Username": c.Username, "url": st["app_url"], "Password": "********"})
			})
			return
		}
	}
	slog.Error("welcome notify", "err", err)
}
