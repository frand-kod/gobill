package web

// Admin login, logout, login throttling, legacy password check and role guards.

import (
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"net/netip"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"context"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
	"golang.org/x/crypto/bcrypt"
)

// ---- login / logout ----

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, http.StatusOK, "login", Page{Title: "Admin sign in"})
}

type loginData struct{ Username string }

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PostFormValue("username"))
	ip, uk := core.ClientIP(r), "u:"+strings.ToLower(username)
	if !s.LoginReserve(ip, username) {
		s.Render(w, r, http.StatusTooManyRequests, "login", Page{
			Title: "Admin sign in",
			Error: "Too many failed attempts. Try again in 15 minutes.",
		})
		return
	}
	s.UserDelay(uk)

	password := r.PostFormValue("password")

	admin, err := s.Queries.GetAdminByUsername(r.Context(), username)
	hash := s.DummyHash // same bcrypt work whether or not the user exists
	if err == nil {
		hash = []byte(admin.PasswordHash)
	}
	passOK := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	passOK = passOK || (err == nil && s.admins().LegacyLogin(r.Context(), admin.ID, password))
	if err != nil || !passOK || admin.Status != "Active" {
		metrics.Inc("login_failures_total", "scope", "admin")
		s.RecordFailure(uk)
		s.Render(w, r, http.StatusOK, "login", Page{
			Title: "Admin sign in",
			Error: "Invalid Username or Password",
			Data:  loginData{Username: username},
		})
		return
	}

	s.LoginForgive(ip, username)
	if admin.TotpEnabled == 1 {
		// Failures are counted in the second step. Clearing them here would let a correct password reset the count.
		if err := s.Sessions.RenewToken(r.Context()); err != nil {
			slog.Error("renew session", "err", err)
			s.ErrorPage(w, "-")
			return
		}
		s.Sessions.Put(r.Context(), "pending_2fa_tries", 0)
		s.Sessions.Put(r.Context(), "pending_2fa", admin.ID)
		s.Sessions.Put(r.Context(), "pending_2fa_until", time.Now().Add(pending2FAWindow).Unix())
		http.Redirect(w, r, "/admin/login/2fa", http.StatusSeeOther)
		return
	}
	s.ClearFailures(core.LoginKey(ip, username))
	s.ClearFailures(uk)
	s.finishLogin(w, r, admin)
}

// finishLogin opens the admin session. Called once the password, and the second factor if any, is verified.
func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, admin db.Admin) {
	if err := s.Sessions.RenewToken(r.Context()); err != nil {
		slog.Error("renew session", "err", err)
		s.ErrorPage(w, "-")
		return
	}
	if s.single.Load() { // single_session: a new login invalidates the other sessions
		var err error
		if admin.SessionVersion, err = s.Queries.BumpAdminSession(r.Context(), admin.ID); err != nil {
			slog.Error("bump session", "err", err)
			s.ErrorPage(w, "-")
			return
		}
	}
	s.Sessions.Put(r.Context(), "admin_id", admin.ID)
	s.Sessions.Put(r.Context(), "sv", admin.SessionVersion)
	if err := s.Queries.TouchAdminLogin(r.Context(), admin.ID); err != nil {
		slog.Error("touch login", "err", err)
	}
	s.fillAppURL(r)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// fillAppURL stores the address of the first successful admin login as app_url, once.
// Only called after authentication: the Host header is attacker-controlled otherwise.
func (s *Server) fillAppURL(r *http.Request) {
	h, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		h = r.Host
	}
	// a LAN/localhost login during setup is not an address customers can open
	if a, e := netip.ParseAddr(strings.Trim(h, "[]")); h == "" || h == "localhost" || e == nil && (a.IsPrivate() || a.IsLoopback()) {
		return
	}
	st, err := s.LoadSettings(r.Context())
	if err != nil || st["app_url"] != "" {
		if err != nil {
			slog.Error("load settings", "err", err)
		}
		return
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" || strings.Contains(r.Header.Get("CF-Visitor"), `"scheme":"https"`) {
		scheme = "https"
	}
	if err := s.Queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: "app_url", Value: scheme + "://" + r.Host}); err != nil {
		slog.Error("save app_url", "err", err)
	}
}

// root sends a customer to the portal and everyone else to the customer login.
func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	to := "/login"
	if s.Sessions.GetInt64(r.Context(), "admin_id") != 0 {
		to = "/admin"
	} else if s.Sessions.GetInt64(r.Context(), "customer_id") != 0 {
		to = "/portal"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Sessions.Destroy(r.Context()); err != nil {
		slog.Error("destroy session", "err", err)
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// requireAdmin returns middleware that needs a logged-in, active admin.
// With roles given, the admin must also have one of them.
func (s *Server) requireAdmin(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := s.Sessions.GetInt64(r.Context(), "admin_id")
			if id == 0 {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			admin, err := s.Queries.GetAdmin(r.Context(), id)
			if err != nil && err != sql.ErrNoRows {
				// A cancelled request or a busy DB is not a reason to log the admin out.
				s.Fail(w, "load admin", err)
				return
			}
			if err != nil || admin.Status != "Active" {
				s.Sessions.Destroy(r.Context())
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			if admin.SessionVersion != s.Sessions.GetInt64(r.Context(), "sv") {
				// password, role or status changed since this session was created
				s.Sessions.Destroy(r.Context())
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			// an admin still on the imported sha1 hash must pick a new password first
			if admin.LegacySha1 != "" && r.URL.Path != "/admin/password" && r.URL.Path != "/logout" {
				s.Sessions.Put(r.Context(), "flash", s.T("Your password is from the old system. Choose a new one to continue."))
				http.Redirect(w, r, "/admin/password", http.StatusSeeOther)
				return
			}
			if len(roles) > 0 && !httpx.Contains(roles, admin.Role) {
				s.deny(w, r, http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, &admin)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func adminFrom(r *http.Request) *db.Admin {
	a, _ := r.Context().Value(ctxKey{}).(*db.Admin)
	return a
}

// bcryptCost is lowered by tests; bcrypt at the default cost dominates test time under -race.
var bcryptCost = bcrypt.DefaultCost
