package web

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Old forgot.php: OTP by WA/SMS, then a new password. The OTP state lives in the
// customer's (anonymous) session; the customer is not logged in during the flow.

const (
	otpTTL   = 10 * time.Minute
	otpTries = 5
)

// newOTP returns a 6-digit code from crypto/rand.
func newOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(n.Int64()+100000, 10), nil
}

// forgotClear drops the whole forgot-password state.
func (s *Server) forgotClear(r *http.Request) {
	for _, k := range []string{"forgot_user", "forgot_hash", "forgot_exp", "forgot_tries", "forgot_ok"} {
		s.sessions.Remove(r.Context(), k)
	}
}

// forgotRender shows the step the session is in: user -> code -> reset.
func (s *Server) forgotRender(w http.ResponseWriter, r *http.Request, status int, flash, msg string) {
	ctx := r.Context()
	user := s.sessions.GetString(ctx, "forgot_user")
	step := "user"
	if user != "" && s.sessions.GetBool(ctx, "forgot_ok") {
		step = "reset"
	} else if user != "" {
		step = "code"
	}
	s.prender(w, r, status, "p_forgot", Page{Title: "Forgot Password", Flash: flash, Error: msg,
		Data: struct{ Step, Username string }{step, user}})
}

func (s *Server) pForgotForm(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("cancel") != "" {
		s.forgotClear(r)
	}
	s.forgotRender(w, r, http.StatusOK, "", "")
}

// pForgotSend starts the flow: a code is generated for every username, but only sent
// when the username and a phone exist, so the answer never shows which accounts exist.
func (s *Server) pForgotSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	username := strings.TrimSpace(r.PostFormValue("username"))
	if username == "" {
		s.forgotRender(w, r, http.StatusOK, "", "This field is required")
		return
	}
	st, err := s.loadSettings(ctx)
	if err != nil {
		s.fail(w, "forgot settings", err)
		return
	}
	if st["sms_url"] == "" && st["wa_url"] == "" {
		s.forgotRender(w, r, http.StatusOK, "", "Password reset is not available, please contact admin")
		return
	}
	if s.sessions.GetString(ctx, "forgot_user") == username && time.Now().Unix() < s.sessions.GetInt64(ctx, "forgot_exp") {
		wait := s.sessions.GetInt64(ctx, "forgot_exp") - time.Now().Unix()
		s.forgotRender(w, r, http.StatusOK, "", "Verification Code already sent, please wait "+strconv.FormatInt(wait, 10)+" seconds.")
		return
	}
	otp, err := newOTP()
	if err != nil {
		s.fail(w, "forgot otp", err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(otp), bcryptCost)
	if err != nil {
		s.fail(w, "forgot hash", err)
		return
	}
	s.sessions.Put(ctx, "forgot_user", username)
	s.sessions.Put(ctx, "forgot_hash", string(hash))
	s.sessions.Put(ctx, "forgot_exp", time.Now().Add(otpTTL).Unix())
	s.sessions.Put(ctx, "forgot_tries", otpTries)
	s.sessions.Remove(ctx, "forgot_ok")

	c, err := s.queries.GetCustomerByUsername(ctx, username)
	phone := ""
	if err == nil {
		phone = c.Phone
	}
	if !s.otpAllow(clientIP(r), phone) { // counts unknown usernames too, so the answer stays uniform
		s.forgotClear(r)
		s.forgotRender(w, r, http.StatusTooManyRequests, "", "Too many verification code requests, please try again later")
		return
	}
	if err == nil && c.Phone != "" {
		if err := s.sendOTP(ctx, st, c.Phone, "Verification code", otp); err != nil {
			slog.Error("send forgot otp", "err", err)
			s.forgotClear(r)
			s.forgotRender(w, r, http.StatusOK, "", "Failed to send verification code")
			return
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.fail(w, "forgot customer", err)
		return
	}
	s.forgotRender(w, r, http.StatusOK, s.catalog.T(s.language(), "If your Username is found, Verification Code has been Sent to Your Phone/Email/Whatsapp"), "")
}

// pForgotVerify checks the code. Each wrong code uses one of otpTries; the last one drops the flow.
func (s *Server) pForgotVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := "f:" + clientIP(r)
	if s.tooManyFailures(ip) {
		s.forgotRender(w, r, http.StatusTooManyRequests, "", "Too many failed attempts. Try again in 15 minutes.")
		return
	}
	if s.sessions.GetString(ctx, "forgot_user") == "" || time.Now().Unix() > s.sessions.GetInt64(ctx, "forgot_exp") {
		s.forgotClear(r)
		s.forgotRender(w, r, http.StatusOK, "", "Invalid Username or Verification Code")
		return
	}
	code := strings.TrimSpace(r.PostFormValue("otp_code"))
	if bcrypt.CompareHashAndPassword([]byte(s.sessions.GetString(ctx, "forgot_hash")), []byte(code)) != nil {
		s.recordFailure(ip)
		tries := s.sessions.GetInt(ctx, "forgot_tries") - 1
		if tries <= 0 {
			s.forgotClear(r)
			s.forgotRender(w, r, http.StatusOK, "", "Too many invalid attempts, please request a new Verification Code")
			return
		}
		s.sessions.Put(ctx, "forgot_tries", tries)
		s.forgotRender(w, r, http.StatusOK, "", "Invalid Username or Verification Code")
		return
	}
	s.clearFailures(ip)
	s.sessions.Put(ctx, "forgot_ok", true)
	s.forgotRender(w, r, http.StatusOK, s.catalog.T(s.language(), "Verification Code Valid"), "")
}

// pForgotReset sets the new password of the verified customer, then sends them to login.
func (s *Server) pForgotReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := s.sessions.GetString(ctx, "forgot_user")
	if user == "" || !s.sessions.GetBool(ctx, "forgot_ok") || time.Now().Unix() > s.sessions.GetInt64(ctx, "forgot_exp") {
		s.forgotClear(r)
		s.forgotRender(w, r, http.StatusOK, "", "Invalid Username or Verification Code")
		return
	}
	npass := r.PostFormValue("npass")
	msg := ""
	switch {
	case len(npass) < 3 || len(npass) > 35:
		msg = "Password should be between 3 to 35 characters"
	case npass != r.PostFormValue("cnpass"):
		msg = "Passwords does not match"
	}
	if msg != "" {
		s.forgotRender(w, r, http.StatusOK, "", msg)
		return
	}
	c, err := s.queries.GetCustomerByUsername(ctx, user)
	if err != nil {
		s.fail(w, "forgot reset", err)
		return
	}
	if err = s.setPassword(ctx, c.ID, npass); err != nil {
		s.fail(w, "forgot reset", err)
		return
	}
	s.Billing.SyncCustomer(ctx, c.ID, 0)
	s.forgotClear(r)
	s.sessions.Put(ctx, "flash", s.catalog.T(s.language(), "Password changed successfully"))
	http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
}
