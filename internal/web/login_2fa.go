package web

// Second login step for admins with 2FA on. The password step leaves only a pending marker in the
// session; the admin session is opened by finishLogin once the code checks out.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
)

// pending2FAWindow is how long a password-verified admin has to enter the second factor.
const pending2FAWindow = 5 * time.Minute

const (
	max2FAPerLogin = 5  // wrong codes before the pending login is dropped
	max2FAFailures = 10 // wrong codes per account (any IP) before only recovery codes work, for the failure window
)

// pending2FA returns the admin id waiting for a second factor, or 0 when none is waiting or it expired.
func (s *Server) pending2FA(r *http.Request) int64 {
	ctx := r.Context()
	if time.Now().Unix() > s.sessions.GetInt64(ctx, "pending_2fa_until") {
		return 0
	}
	return s.sessions.GetInt64(ctx, "pending_2fa")
}

func (s *Server) login2FAForm(w http.ResponseWriter, r *http.Request) {
	if s.pending2FA(r) == 0 {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login_2fa", Page{Title: "Two-factor login"})
}

func (s *Server) login2FASubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := s.pending2FA(r)
	if id == 0 {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	admin, err := s.queries.GetAdmin(ctx, id)
	if err != nil || admin.Status != "Active" || admin.TotpEnabled != 1 {
		s.sessions.Remove(ctx, "pending_2fa")
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	// Same throttle keys as the password step: 2FA guesses count against the IP and the account.
	ip, uk := clientIP(r), "u:"+strings.ToLower(admin.Username)
	if !s.loginReserve(ip, admin.Username) {
		s.render(w, r, http.StatusTooManyRequests, "login_2fa", Page{
			Title: "Two-factor login",
			Error: "Too many failed attempts. Try again in 15 minutes.",
		})
		return
	}
	s.userDelay(uk)
	ak, code := fmt.Sprint("2fa:", id), cleanCode(r.PostFormValue("code"))
	s.mu.Lock()
	hardLocked := len(s.recent(ak)) >= max2FAFailures
	s.mu.Unlock()
	var ok bool
	if hardLocked { // only a recovery code gets through while the account is locked
		ok = s.useRecoveryCode(r, admin.ID, code)
	} else {
		ok = s.verify2FA(r, admin, code)
	}
	if !ok {
		metrics.Inc("login_failures_total", "scope", "2fa")
		s.recordFailure(uk)
		s.recordFailure(ak)
		if tries := s.sessions.GetInt64(ctx, "pending_2fa_tries") + 1; tries >= max2FAPerLogin {
			s.sessions.Remove(ctx, "pending_2fa")
			s.sessions.Remove(ctx, "pending_2fa_until")
			s.sessions.Remove(ctx, "pending_2fa_tries")
			s.render(w, r, http.StatusOK, "login", Page{Title: "Admin sign in", Error: "Too many wrong codes. Sign in again."})
			return
		} else {
			s.sessions.Put(ctx, "pending_2fa_tries", tries)
		}
		s.render(w, r, http.StatusOK, "login_2fa", Page{Title: "Two-factor login", Error: "Invalid code"})
		return
	}
	s.loginForgive(ip, admin.Username)
	s.clearFailures(loginKey(ip, admin.Username))
	s.clearFailures(uk)
	s.clearFailures(ak)
	s.sessions.Remove(ctx, "pending_2fa_tries")
	s.sessions.Remove(ctx, "pending_2fa")
	s.sessions.Remove(ctx, "pending_2fa_until")
	s.finishLogin(w, r, admin)
}

// verify2FA accepts the current TOTP code or an unused recovery code.
func (s *Server) verify2FA(r *http.Request, admin db.Admin, code string) bool {
	key, err := s.totpOpen(admin.TotpSecretEnc)
	if err != nil {
		return false
	}
	return s.totpVerify(admin.ID, key, code) || s.useRecoveryCode(r, admin.ID, code)
}
