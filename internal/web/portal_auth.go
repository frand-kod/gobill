package web

// Customer portal login, logout, session guard and page rendering.

import (
	"log/slog"
	"net/http"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"unicode"
)

// Customer portal. The session key "customer_id" is separate from the admin's "admin_id".

type custCtxKey struct{}

func customerFrom(r *http.Request) *db.Customer {
	c, _ := r.Context().Value(custCtxKey{}).(*db.Customer)
	return c
}

func (s *Server) requireCustomer(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := s.sessions.GetInt64(r.Context(), "customer_id")
		c, err := s.queries.GetCustomer(r.Context(), id)
		if id == 0 || err != nil || c.Status == "Banned" || c.Status == "Disabled" || c.SessionVersion != s.sessions.GetInt64(r.Context(), "csv") {
			s.sessions.Remove(r.Context(), "customer_id")
			http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), custCtxKey{}, &c)))
	})
}

func (s *Server) prender(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	p.Customer = customerFrom(r)
	if p.Customer != nil {
		p.Unread, _ = s.queries.CountUnreadInbox(r.Context(), p.Customer.ID)
		p.Impersonating = s.sessions.GetInt64(r.Context(), "impersonator") != 0
	}
	if p.Flash == "" {
		p.Flash = s.sessions.PopString(r.Context(), "flash")
	}
	s.render(w, r, status, name, p)
}

// ---- login ----

func (s *Server) pLoginForm(w http.ResponseWriter, r *http.Request) {
	s.prender(w, r, 200, "p_login", Page{Title: "Sign in"})
}

func (s *Server) pLogin(w http.ResponseWriter, r *http.Request) {
	ip := "c:" + clientIP(r)
	if s.tooManyFailures(ip) {
		s.prender(w, r, http.StatusTooManyRequests, "p_login", Page{Title: "Sign in", Error: "Too many failed attempts. Try again in 15 minutes."})
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	c, err := s.queries.GetCustomerByUsername(r.Context(), username)
	hash := s.dummyHash
	if err == nil {
		hash = []byte(c.PasswordHash)
	}
	ok := bcrypt.CompareHashAndPassword(hash, []byte(r.PostFormValue("password"))) == nil
	if err != nil || !ok || c.Status == "Banned" || c.Status == "Disabled" {
		s.recordFailure(ip)
		s.prender(w, r, 200, "p_login", Page{Title: "Sign in", Error: "Invalid Username or Password", Data: username})
		return
	}
	s.clearFailures(ip)
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		s.fail(w, "renew session", err)
		return
	}
	s.sessions.Put(r.Context(), "customer_id", c.ID)
	s.sessions.Put(r.Context(), "csv", c.SessionVersion)
	if err := s.queries.TouchCustomerLogin(r.Context(), c.ID); err != nil {
		slog.Error("touch login", "err", err)
	}
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// keepSession re-stamps the acting session with the customer's bumped session_version.
func (s *Server) keepSession(r *http.Request, id int64) error {
	c, err := s.queries.GetCustomer(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		return err
	}
	s.sessions.Put(r.Context(), "csv", c.SessionVersion)
	return nil
}

func (s *Server) pLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.Remove(r.Context(), "customer_id")
	s.sessions.Remove(r.Context(), "impersonator")
	http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
}

// ---- register ----

// pVoucherLogin is the old login/activation: in "Voucher Only" mode a username plus a voucher code
// creates the customer (password and PPPoE secret = the code) when missing, then redeems the voucher.
// An existing customer keeps their password (old PHP overwrote it with the code).
func (s *Server) pVoucherLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.loadSettings(ctx)
	if err != nil || s.Billing == nil {
		s.fail(w, "voucher login", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["disable_registration"] != "yes" {
		s.flashTo(w, r, "/portal/login", "Registration Disabled")
		return
	}
	ip := "c:" + clientIP(r)
	bad := func() {
		s.recordFailure(ip)
		s.prender(w, r, 200, "p_login", Page{Title: "Sign in", Error: "Voucher Not Valid"})
	}
	code := strings.TrimSpace(r.PostFormValue("voucher"))
	username := strings.Map(func(c rune) rune {
		if c < 128 && (unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("+_.@-", c)) {
			return c
		}
		return -1
	}, r.PostFormValue("username"))
	if s.tooManyFailures(ip) || code == "" || len(username) < 3 || len(username) > 55 {
		bad()
		return
	}
	if v, err := s.queries.GetVoucherByCode(ctx, code); err != nil || v.Status != "unused" {
		bad()
		return
	}
	c, err := s.queries.GetCustomerByUsername(ctx, username)
	if err != nil {
		hash, herr := bcrypt.GenerateFromPassword([]byte(code), bcryptCost)
		enc, serr := secret.Seal(s.SecretKey, []byte(code))
		if herr != nil || serr != nil {
			s.fail(w, "voucher login", errors.Join(herr, serr))
			return
		}
		phone := ""
		if len(username) < 21 {
			phone = username
		}
		if c, err = s.queries.CreateCustomer(ctx, db.CreateCustomerParams{Username: username, PasswordHash: string(hash),
			Phone: phone, ServiceType: "Others", SecretEnc: enc, AutoRenewal: 1, Status: "Active"}); err != nil {
			s.fail(w, "voucher login", err)
			return
		}
	}
	switch err := s.Billing.RedeemVoucher(ctx, code, c.ID); {
	case errors.Is(err, billing.ErrVoucherInvalid):
		bad()
	case errors.Is(err, billing.ErrInactive):
		s.flashTo(w, r, "/portal/login", "account is not active")
	case err != nil:
		s.fail(w, "voucher login", err)
	default:
		to := "/portal/login"
		if u := safeRedirect(st["voucher_redirect"]); u != "" {
			to = u
		}
		s.flashTo(w, r, to, "Voucher activation success, now you can login")
	}
}
