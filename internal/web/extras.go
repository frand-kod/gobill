package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
)

// Customer actions, login-as, welcome message, portal activation list / buy for friend /
// forgot username, and log cleanup.

func (s *Server) extraRoutes(mux *http.ServeMux, managers func(http.Handler) http.Handler) {
	mux.Handle("POST /admin/customers/{id}/deactivate", managers(http.HandlerFunc(s.custDeactivate)))
	mux.Handle("POST /admin/customers/{id}/sync", managers(http.HandlerFunc(s.custSync)))
	mux.Handle("POST /admin/customers/{id}/login", managers(http.HandlerFunc(s.custLoginAs)))
	mux.Handle("POST /admin/logs/clean/{kind}", managers(http.HandlerFunc(s.logClean)))

	mux.HandleFunc("POST /portal/impersonate/end", s.pImpersonateEnd)
	mux.Handle("GET /portal/activation", s.requireCustomer(s.pActivations))
	mux.Handle("GET /portal/plans/{id}/friend", s.requireCustomer(s.pFriendForm))
	mux.Handle("POST /portal/plans/{id}/friend", s.requireCustomer(s.pFriend))
	mux.HandleFunc("GET /portal/forgot/username", s.pForgotUserForm)
	mux.HandleFunc("POST /portal/forgot/username", s.pForgotUser)
}

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

// ---- portal ----

func (s *Server) pActivations(w http.ResponseWriter, r *http.Request) {
	trx, err := s.queries.ListTransactionsByCustomer(r.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}, Limit: 200})
	if err != nil {
		s.fail(w, "portal activations", err)
		return
	}
	var out []db.Transaction
	for _, t := range trx {
		if t.Type != "Balance" { // balance moves are not activations
			out = append(out, t)
		}
	}
	s.prender(w, r, 200, "p_activation", Page{Title: "Activation History", Data: out})
}

type friendData struct {
	Plan     db.Plan
	Username string
}

func (s *Server) friendPage(w http.ResponseWriter, r *http.Request, code int, username, errMsg string) {
	p, err := s.queries.GetPlan(r.Context(), pathID(r))
	if err != nil || p.Enabled != 1 || p.Type == "Balance" || p.Billing != "prepaid" {
		http.NotFound(w, r)
		return
	}
	s.prender(w, r, code, "p_friend", Page{Title: "Buy for friend", Error: errMsg, Data: friendData{p, username}})
}

func (s *Server) pFriendForm(w http.ResponseWriter, r *http.Request) {
	s.friendPage(w, r, 200, strings.TrimSpace(r.URL.Query().Get("u")), "")
}

func (s *Server) pFriend(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.fail(w, "portal friend", errors.New("billing not configured"))
		return
	}
	user := strings.TrimSpace(r.PostFormValue("username"))
	err := s.Billing.SendPlan(r.Context(), customerFrom(r).ID, user, pathID(r))
	for _, e := range []error{billing.ErrSelfTransfer, billing.ErrTargetNotFound, billing.ErrInsufficientBalance, billing.ErrInactive,
		billing.ErrTransferDisabled, billing.ErrPlanNotFound, billing.ErrFriendPlanDiffers} {
		if errors.Is(err, e) {
			s.friendPage(w, r, 200, user, s.catalog.T(s.language(), e.Error()))
			return
		}
	}
	if err != nil {
		s.fail(w, "portal friend", err)
		return
	}
	s.flashTo(w, r, "/portal/activation", "Success to send package")
}

func (s *Server) pForgotUserForm(w http.ResponseWriter, r *http.Request) {
	s.prender(w, r, 200, "p_forgot_user", Page{Title: "Forgot Usernames"})
}

// pForgotUser is old forgot step 6: the usernames registered with an email or phone are sent to
// that contact. The answer is the same whether or not anything matched (no account enumeration);
// sending runs in the background so timing does not tell either.
func (s *Server) pForgotUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	contact := strings.TrimSpace(r.PostFormValue("contact"))
	isMail := strings.Contains(contact, "@")
	if contact == "" || !isMail && len(normPhone(contact)) < 6 {
		s.prender(w, r, 200, "p_forgot_user", Page{Title: "Forgot Usernames", Error: "Invalid phone number format"})
		return
	}
	if !s.otpAllow(clientIP(r), strings.ToLower(contact)) {
		s.prender(w, r, http.StatusTooManyRequests, "p_forgot_user", Page{Title: "Forgot Usernames", Error: "Too many verification code requests, please try again later"})
		return
	}
	st, err := s.loadSettings(ctx)
	if err != nil {
		s.fail(w, "forgot usernames", err)
		return
	}
	// ponytail: lookup uses the 50 best LIKE matches, so a phone stored with other separators than
	// the typed one may be missed; normalise phones in the DB if that shows up.
	var names []string
	for _, q := range []string{contact, normPhone(contact)} {
		rows, err := s.queries.SearchCustomers(ctx, db.SearchCustomersParams{Q: q, PageLimit: 50})
		if err != nil {
			s.fail(w, "forgot usernames", err)
			return
		}
		for _, c := range rows {
			if (isMail && strings.EqualFold(c.Email, contact) || !isMail && c.Phone != "" && normPhone(c.Phone) == normPhone(contact)) && !contains(names, c.Username) {
				names = append(names, c.Username)
			}
		}
	}
	if len(names) > 0 {
		list, lang := strings.Join(names, ", "), s.language()
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var err error
			if isMail {
				var n *notify.Notifier
				if n, err = notify.Load(bg, s.queries); err == nil {
					n.Log = notify.LogTo(s.queries)
					err = n.Email(bg, contact, "["+st["company_name"]+"] "+s.catalog.T(lang, "Your usernames"), list)
				}
			} else {
				err = s.sendOTP(bg, st, normPhone(contact), "Your usernames", list)
			}
			if err != nil {
				slog.Error("send forgot usernames", "err", err)
			}
		}()
	}
	s.prender(w, r, 200, "p_forgot_user", Page{Title: "Forgot Usernames",
		Flash: s.catalog.T(s.language(), "If the contact is registered, the usernames have been sent")})
}

// ---- log cleanup ----

var logPages = map[string]string{"activity": "/admin/logs", "radius": "/admin/logs/radius", "messages": "/admin/logs/messages"}

func (s *Server) logClean(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	back, ok := logPages[kind]
	if !ok {
		http.NotFound(w, r)
		return
	}
	days, ok := posInt(strings.TrimSpace(r.PostFormValue("keep")))
	if !ok || days > 36500 || s.Billing == nil {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Enter a number greater than 0"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	n, err := s.Billing.CleanLog(r.Context(), kind, int(days))
	if err != nil {
		s.fail(w, "clean logs", err)
		return
	}
	s.logActivity(r, "logs.clean", fmt.Sprintf("%s older than %d days: %d deleted", kind, days, n))
	s.sessions.Put(r.Context(), "flash", fmt.Sprintf("%s %d: %d", s.catalog.T(s.language(), "Deleted logs older than (days)"), days, n))
	http.Redirect(w, r, back, http.StatusSeeOther)
}
