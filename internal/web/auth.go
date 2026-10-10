package web

// Admin login, logout, login throttling, legacy password check and role guards.

import (
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"net/http"

	"context"
	"github.com/frand-kod/gobill/internal/db"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"time"
)

// ---- login / logout ----

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "login", Page{Title: "Sign in"})
}

type loginData struct{ Username string }

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PostFormValue("username"))
	ip, uk := clientIP(r), "u:"+strings.ToLower(username)
	if s.tooManyFailures(ip) || s.tooManyFailures(uk) {
		s.render(w, r, http.StatusTooManyRequests, "login", Page{
			Title: "Sign in",
			Error: "Too many failed attempts. Try again in 15 minutes.",
		})
		return
	}

	password := r.PostFormValue("password")

	admin, err := s.queries.GetAdminByUsername(r.Context(), username)
	hash := s.dummyHash // same bcrypt work whether or not the user exists
	if err == nil {
		hash = []byte(admin.PasswordHash)
	}
	passOK := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	passOK = passOK || (err == nil && s.legacyLogin(r.Context(), admin.ID, password))
	if err != nil || !passOK || admin.Status != "Active" {
		s.recordFailure(ip)
		s.recordFailure(uk)
		s.render(w, r, http.StatusOK, "login", Page{
			Title: "Sign in",
			Error: "Invalid Username or Password",
			Data:  loginData{Username: username},
		})
		return
	}

	s.clearFailures(ip)
	s.clearFailures(uk)
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		slog.Error("renew session", "err", err)
		s.errorPage(w, "-")
		return
	}
	if s.single.Load() { // single_session: a new login invalidates the other sessions
		if admin.SessionVersion, err = s.queries.BumpAdminSession(r.Context(), admin.ID); err != nil {
			slog.Error("bump session", "err", err)
			s.errorPage(w, "-")
			return
		}
	}
	s.sessions.Put(r.Context(), "admin_id", admin.ID)
	s.sessions.Put(r.Context(), "sv", admin.SessionVersion)
	if err := s.queries.TouchAdminLogin(r.Context(), admin.ID); err != nil {
		slog.Error("touch login", "err", err)
	}
	s.fillAppURL(r)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// fillAppURL stores the address of the first successful admin login as app_url, once.
// Only called after authentication: the Host header is attacker-controlled otherwise.
func (s *Server) fillAppURL(r *http.Request) {
	if r.Host == "" {
		return
	}
	st, err := s.loadSettings(r.Context())
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
	if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: "app_url", Value: scheme + "://" + r.Host}); err != nil {
		slog.Error("save app_url", "err", err)
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Destroy(r.Context()); err != nil {
		slog.Error("destroy session", "err", err)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) tooManyFailures(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recent(ip)) >= maxFailedLogins
}

func (s *Server) recordFailure(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed[ip] = append(s.recent(ip), time.Now())
}

func (s *Server) clearFailures(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failed, ip)
}

// recent drops failures older than the window. Caller holds s.mu.
func (s *Server) recent(ip string) []time.Time {
	cutoff := time.Now().Add(-loginWindow)
	list := s.failed[ip][:0]
	for _, t := range s.failed[ip] {
		if t.After(cutoff) {
			list = append(list, t)
		}
	}
	if len(list) == 0 {
		delete(s.failed, ip)
	} else {
		s.failed[ip] = list
	}
	return list
}

// legacyLogin checks an admin imported from PHPNuxBill (sha1 in admins.legacy_sha1).
// On a match it rehashes the password to bcrypt and clears the marker, so it works once.
func (s *Server) legacyLogin(ctx context.Context, id int64, password string) bool {
	var want string
	if s.conn.QueryRowContext(ctx, "SELECT legacy_sha1 FROM admins WHERE id = ?", id).Scan(&want) != nil || want == "" {
		return false
	}
	sum := sha1.Sum([]byte(password))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) != 1 {
		return false
	}
	if h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost); err == nil {
		s.conn.ExecContext(ctx, "UPDATE admins SET password_hash = ?, legacy_sha1 = '' WHERE id = ?", string(h), id)
	}
	return true
}

// requireAdmin returns middleware that needs a logged-in, active admin.
// With roles given, the admin must also have one of them.
func (s *Server) requireAdmin(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := s.sessions.GetInt64(r.Context(), "admin_id")
			if id == 0 {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			admin, err := s.queries.GetAdmin(r.Context(), id)
			if err != nil || admin.Status != "Active" {
				if err != nil && err != sql.ErrNoRows {
					slog.Error("load admin", "err", err)
				}
				s.sessions.Destroy(r.Context())
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			if admin.SessionVersion != s.sessions.GetInt64(r.Context(), "sv") {
				// password, role or status changed since this session was created
				s.sessions.Destroy(r.Context())
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			if len(roles) > 0 && !contains(roles, admin.Role) {
				http.Error(w, "Forbidden", http.StatusForbidden)
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
