package web

// Customer forgot-password and forgot-username flow.

import (
	"database/sql"
	"log/slog"
	"net/http"

	"context"
	"errors"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"golang.org/x/crypto/bcrypt"
	"strconv"
	"strings"
	"time"
)

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
	if len(names) > 0 && !notify.CustomersOff(st) {
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

// Old forgot.php: OTP by WA/SMS, then a new password. The OTP state lives in the
// customer's (anonymous) session; the customer is not logged in during the flow.

const (
	otpTTL   = 10 * time.Minute
	otpTries = 5
)

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
	if st["sms_url"] == "" && !notify.WAConfigured(st) {
		s.forgotRender(w, r, http.StatusOK, "", "Password reset is not available, please contact admin")
		return
	}
	if otpOff(st) { // same answer for every username
		s.forgotRender(w, r, http.StatusOK, "", "Verification code is not available right now")
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
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.fail(w, "forgot customer", err)
		return
	}
	found := err == nil && c.Phone != ""
	// The rate limit key is the phone when there is one, else the username, so a known and an
	// unknown username get the same limits and the 429 does not reveal which exists.
	key := username
	if found {
		key = c.Phone
	}
	if !s.otpAllow(clientIP(r), key) {
		s.forgotClear(r)
		s.forgotRender(w, r, http.StatusTooManyRequests, "", "Too many verification code requests, please try again later")
		return
	}
	if found {
		// Sent in the background, like pForgotUser, so timing and send failures do not reveal the account.
		phone := c.Phone
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.sendOTP(bg, st, phone, "Verification code", otp); err != nil {
				slog.Error("send forgot otp", "err", err)
			}
		}()
	}
	s.forgotRender(w, r, http.StatusOK, s.catalog.T(s.language(), "If your Username is found, Verification Code has been Sent to Your Phone/Email/Whatsapp"), "")
}

// pForgotVerify checks the code. Each wrong code uses one of otpTries; the last one drops the flow.
func (s *Server) pForgotVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := "f:" + clientIP(r)
	if st, err := s.loadSettings(ctx); err != nil {
		s.fail(w, "forgot verify settings", err)
		return
	} else if otpOff(st) {
		s.forgotRender(w, r, http.StatusOK, "", "Verification code is not available right now")
		return
	}
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
	case len(npass) < minPasswordLen || len(npass) > 35:
		msg = "Password should be between 8 to 35 characters"
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
