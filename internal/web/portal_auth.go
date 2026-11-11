package web

// Customer portal login, logout, session guard and page rendering.

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/secret"
	"golang.org/x/crypto/bcrypt"
)

// Customer portal. The session key "customer_id" is separate from the admin's "admin_id".

type custCtxKey struct{}

func customerFrom(r *http.Request) *db.Customer {
	c, _ := r.Context().Value(custCtxKey{}).(*db.Customer)
	return c
}

func (s *Server) requireCustomer(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := s.Sessions.GetInt64(r.Context(), "customer_id")
		c, err := s.Queries.GetCustomer(r.Context(), id)
		if id != 0 && err != nil && err != sql.ErrNoRows {
			s.Fail(w, "load customer", err) // a cancelled request or a busy DB keeps the session
			return
		}
		if id == 0 || err != nil || c.Status == "Banned" || c.Status == "Disabled" || c.SessionVersion != s.Sessions.GetInt64(r.Context(), "csv") {
			s.Sessions.Remove(r.Context(), "customer_id")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), custCtxKey{}, &c)))
	})
}

func (s *Server) prender(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	p.Customer = customerFrom(r)
	if p.Customer != nil {
		var err error
		p.Unread, _ = s.Queries.CountUnreadInbox(r.Context(), p.Customer.ID)
		if p.Bell, err = s.portalBell(r, p.Customer); err != nil {
			slog.Warn("portal bell", "err", err)
		}
		for _, b := range p.Bell {
			if b.Unread {
				p.BellUnread++
			}
		}
		p.Impersonating = s.Sessions.GetInt64(r.Context(), "impersonator") != 0
	}
	if p.Flash == "" {
		p.Flash = s.Sessions.PopString(r.Context(), "flash")
	}
	s.Render(w, r, status, name, p)
}

// ---- login ----

func (s *Server) pLoginForm(w http.ResponseWriter, r *http.Request) {
	s.prender(w, r, 200, "p_login", Page{Title: "Customer sign in"})
}

func (s *Server) pLogin(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PostFormValue("username"))
	ip, uk := "c:"+core.ClientIP(r), "cu:"+strings.ToLower(username)
	if !s.LoginReserve(ip, username) {
		s.prender(w, r, http.StatusTooManyRequests, "p_login", Page{Title: "Customer sign in", Error: "Too many failed attempts. Try again in 15 minutes."})
		return
	}
	s.UserDelay(uk)
	c, err := s.Queries.GetCustomerByUsername(r.Context(), username)
	hash := s.DummyHash
	if err == nil {
		hash = []byte(c.PasswordHash)
	}
	ok := bcrypt.CompareHashAndPassword(hash, []byte(r.PostFormValue("password"))) == nil
	if err == nil && ok && (c.Status == "Banned" || c.Status == "Disabled") {
		// only after a correct password, so this does not reveal which usernames exist
		s.LoginForgive(ip, username)
		s.prender(w, r, 200, "p_login", Page{Title: "Customer sign in", Error: "This account cannot be used, contact the admin", Data: username})
		return
	}
	if err != nil || !ok {
		metrics.Inc("login_failures_total", "scope", "portal")
		s.RecordFailure(uk)
		s.prender(w, r, 200, "p_login", Page{Title: "Customer sign in", Error: "Invalid Username or Password", Data: username})
		return
	}
	s.LoginForgive(ip, username)
	s.ClearFailures(core.LoginKey(ip, username))
	s.ClearFailures(uk)
	if err := s.portalSignIn(r, c); err != nil {
		s.Fail(w, "renew session", err)
		return
	}
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// portalSignIn starts a fresh portal session for the customer.
func (s *Server) portalSignIn(r *http.Request, c db.Customer) error {
	if err := s.Sessions.RenewToken(r.Context()); err != nil {
		return err
	}
	s.Sessions.Put(r.Context(), "customer_id", c.ID)
	s.Sessions.Put(r.Context(), "csv", c.SessionVersion)
	if err := s.Queries.TouchCustomerLogin(r.Context(), c.ID); err != nil {
		slog.Error("touch login", "err", err)
	}
	return nil
}

// keepSession re-stamps the acting session with the customer's bumped session_version.
func (s *Server) keepSession(r *http.Request, id int64) error {
	c, err := s.Queries.GetCustomer(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.Sessions.RenewToken(r.Context()); err != nil {
		return err
	}
	s.Sessions.Put(r.Context(), "csv", c.SessionVersion)
	return nil
}

func (s *Server) pLogout(w http.ResponseWriter, r *http.Request) {
	s.Sessions.Remove(r.Context(), "customer_id")
	s.Sessions.Remove(r.Context(), "impersonator")
	for _, k := range s.Sessions.Keys(r.Context()) { // pending contact-change OTP (ct_*) must not survive logout
		if strings.HasPrefix(k, "ct_") {
			s.Sessions.Remove(r.Context(), k)
		}
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- register ----

// pVoucherLogin is the old login/activation: in "Voucher Only" mode a username plus a voucher code
// creates the customer (password and PPPoE secret = the code) when missing, then redeems the voucher.
// An existing customer keeps their password (old PHP overwrote it with the code).
func (s *Server) pVoucherLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.LoadSettings(ctx)
	if err != nil || s.Billing == nil {
		s.Fail(w, "voucher login", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["disable_registration"] != "yes" {
		s.FlashTo(w, r, "/login", "Registration Disabled")
		return
	}
	ip := "c:" + core.ClientIP(r)
	bad := func() {
		s.RecordFailure(ip)
		s.prender(w, r, 200, "p_login", Page{Title: "Customer sign in", Error: "Voucher Not Valid"})
	}
	code := strings.TrimSpace(r.PostFormValue("voucher"))
	username := strings.Map(func(c rune) rune {
		if c < 128 && (unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("+_.@-", c)) {
			return c
		}
		return -1
	}, r.PostFormValue("username"))
	if s.TooManyFailures(ip) || code == "" || len(username) < 3 || len(username) > 55 {
		bad()
		return
	}
	if v, err := s.Queries.GetVoucherByCode(ctx, code); err != nil || v.Status != "unused" {
		bad()
		return
	}
	c, err := s.Queries.GetCustomerByUsername(ctx, username)
	if err != nil {
		hash, herr := bcrypt.GenerateFromPassword([]byte(code), core.BcryptCost)
		enc, serr := secret.Seal(s.SecretKey, []byte(code))
		if herr != nil || serr != nil {
			s.Fail(w, "voucher login", errors.Join(herr, serr))
			return
		}
		phone := ""
		if len(username) < 21 && strings.Trim(username, "+0123456789 -()") == "" { // a phone number, not a voucher code
			phone = db.CanonicalPhone(username)
		}
		if taken, err := s.phoneTaken(ctx, phone, 0); err != nil { // a number another account uses is not taken twice
			s.Fail(w, "voucher login", err)
			return
		} else if taken {
			phone = ""
		}
		if c, err = s.Queries.CreateCustomer(ctx, db.CreateCustomerParams{Username: username, PasswordHash: string(hash),
			Phone: phone, ServiceType: "Others", SecretEnc: enc, AutoRenewal: 1, Status: "Active"}); err != nil {
			s.Fail(w, "voucher login", err)
			return
		}
	}
	switch err := s.Billing.RedeemVoucher(ctx, code, c.ID); {
	case errors.Is(err, billing.ErrVoucherInvalid):
		bad()
	case errors.Is(err, billing.ErrInactive):
		s.FlashTo(w, r, "/login", "account is not active")
	case err != nil:
		s.Fail(w, "voucher login", err)
	default:
		to := "/login"
		if u := httpx.SafeRedirect(st["voucher_redirect"]); u != "" {
			to = u
		}
		s.FlashTo(w, r, to, "Voucher activation success, now you can login")
	}
}
