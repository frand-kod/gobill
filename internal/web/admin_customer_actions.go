package web

// Customer actions: deactivate, sync and welcome message.

import (
	"log/slog"
	"net/http"

	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/web/core"
)

// ---- customer detail actions ----

// custAct runs a billing action for the customer and redirects back to the detail page.
func (s *Server) custAct(w http.ResponseWriter, r *http.Request, action, okMsg string, do func(ctx context.Context, id, adminID int64) (int, error)) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	if s.Billing == nil {
		s.Fail(w, action, errors.New("billing service not configured"))
		return
	}
	back := fmt.Sprint("/admin/customers/", c.ID)
	n, err := do(r.Context(), c.ID, adminFrom(r).ID)
	switch {
	case err != nil:
		slog.Error(action, "customer", c.Username, "err", err)
		s.Sessions.Put(r.Context(), "error", s.T("Action failed"))
		http.Redirect(w, r, back, http.StatusSeeOther)
	case n == 0:
		s.Sessions.Put(r.Context(), "error", s.T("Cannot find active plan"))
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

// welcome sends welcome_message on the chosen channels, in the background. The password is the
// masked "********" unless send_credentials_on_create is yes, then the real one (pass). The
// message log keeps the mask, never the password.
func (s *Server) welcome(ctx context.Context, c db.Customer, vias []string, pass string) {
	if len(vias) == 0 {
		return
	}
	st, err := s.LoadSettings(ctx)
	if err == nil {
		var n *notify.Notifier
		if n, err = notify.Load(ctx, s.Queries); err == nil {
			pw, logTo := "********", notify.LogTo(s.Queries)
			n.Log = logTo
			if st["send_credentials_on_create"] == "yes" && pass != "" {
				pw = pass
				n.Log = func(ch, to, subject, body string, err error) {
					logTo(ch, to, subject, strings.ReplaceAll(body, pass, "********"), err)
				}
			}
			n.Go("welcome", func(ctx context.Context) error {
				return n.CustomOn(ctx, c, vias, "welcome_message", "Welcome", map[string]string{"company": st["company_name"],
					"Username": c.Username, "url": core.CustomerLoginURL(st), "Password": pw})
			})
			return
		}
	}
	slog.Error("welcome notify", "err", err)
}

// custRecountUsage raises the used data and time of the customer's active subscriptions to what the
// sessions still in the log hold: for after a downgrade to a version without the counters, when the
// diagnosis shows the log ahead. It never lowers a counter, so a pruned log cannot give quota back.
func (s *Server) custRecountUsage(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	subs, err := s.Queries.ListSubscriptionsByCustomer(r.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 100})
	if err != nil {
		s.Fail(w, "usage recount", err)
		return
	}
	for _, sub := range subs {
		if sub.Status != "active" {
			continue
		}
		if _, err := s.Queries.RecountSubscriptionUsage(r.Context(), sub.ID); err != nil {
			s.Fail(w, "usage recount", err)
			return
		}
	}
	s.done(w, r, fmt.Sprint("/admin/customers/", c.ID), "Usage recounted from the session log", "customer.usage_recount", c.Username)
}
