package web

// Customer self-registration: username, password and WhatsApp number, with optional WA OTP.

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"log/slog"
	"math/big"
	"net/http"

	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/secret"
	"golang.org/x/crypto/bcrypt"
	"strconv"
	"strings"
	"time"
)

type regData struct {
	Username, Phone string
	OTP             bool // OTP field shown
}

// commonPasswords are refused for every customer password (compared lower case).
var commonPasswords = map[string]bool{"1234": true, "12345": true, "123456": true, "0000": true, "1111": true, "2222": true,
	"1212": true, "4321": true, "abcd": true, "qwer": true, "asdf": true, "password": true, "admin": true, "1q2w": true,
	"aaaa": true, "qwerty": true, "123123": true, "112233": true}

// customerPasswordMin is the setting customer_password_min (4 by default, 4 to 64).
func customerPasswordMin(st map[string]string) int {
	if n, err := strconv.Atoi(st["customer_password_min"]); err == nil && n >= 4 && n <= 64 {
		return n
	}
	return 4
}

// customerPasswordErr returns why pass cannot be a customer password, or "". It applies to every
// customer password path. Equal to the username is refused because RADIUS treats it as a voucher login.
func customerPasswordErr(st map[string]string, username, pass string) string {
	if n := customerPasswordMin(st); len(pass) < n || len(pass) > 64 {
		return fmt.Sprintf("Password should be between %d to 64 characters", n)
	}
	if strings.EqualFold(pass, username) {
		return "Password must not be the same as the username"
	}
	if commonPasswords[strings.ToLower(pass)] {
		return "Password terlalu mudah ditebak"
	}
	return ""
}

// validRegUsername: 4 to 32 lower case letters or digits.
func validRegUsername(u string) bool {
	if len(u) < 4 || len(u) > 32 {
		return false
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (s *Server) pRegisterForm(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "register", err)
		return
	}
	if st["disable_registration"] != "" && st["disable_registration"] != "no" {
		s.flashTo(w, r, "/portal/login", "Registration Disabled")
		return
	}
	s.prender(w, r, 200, "p_register", Page{Title: "Register", Data: regFlags(regData{}, st)})
}

func regFlags(d regData, st map[string]string) regData {
	d.OTP = otpEnabled(st)
	return d
}

func (s *Server) pRegister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.loadSettings(ctx)
	if err != nil {
		s.fail(w, "register", err)
		return
	}
	if st["disable_registration"] != "" && st["disable_registration"] != "no" {
		s.flashTo(w, r, "/portal/login", "Registration Disabled")
		return
	}
	f := r.PostFormValue
	d := regFlags(regData{Username: strings.ToLower(strings.TrimSpace(f("username"))), Phone: normPhone(f("phone_number"))}, st)
	show := func(msg string) {
		s.prender(w, r, 200, "p_register", Page{Title: "Register", Data: d, Error: msg})
	}
	if !validRegUsername(d.Username) {
		show("Username should be 4 to 32 characters, lowercase letters and digits only")
		return
	}
	if len(d.Phone) < 10 {
		show("Invalid phone number format")
		return
	}
	if msg := customerPasswordErr(st, d.Username, f("password")); msg != "" {
		show(msg)
		return
	}
	// RADIUS logs in by username or pppoe_username, and voucher codes are logins too.
	if _, err := s.queries.FindLoginNameFold(ctx, d.Username); err == nil {
		show("Username already used")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.fail(w, "register", err)
		return
	}
	if _, err := s.queries.FindVoucherCodeFold(ctx, d.Username); err == nil {
		show("Username already used")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.fail(w, "register", err)
		return
	}
	if taken, err := s.contactTaken(r, "phone", d.Phone, 0); err != nil {
		s.fail(w, "register", err)
		return
	} else if taken {
		show("Already registered by another customer")
		return
	}
	if d.OTP {
		if otpOff(st) {
			show("Verification code is not available right now")
			return
		}
		if f("send_otp") != "" {
			if !s.otpAllow(clientIP(r), d.Phone) {
				show("Too many verification code requests, please try again later")
				return
			}
			n, _ := rand.Int(rand.Reader, big.NewInt(900000))
			otp := strconv.FormatInt(n.Int64()+100000, 10)
			s.sessions.Put(ctx, "reg_otp", otp)
			s.sessions.Put(ctx, "reg_otp_phone", d.Phone)
			s.sessions.Put(ctx, "reg_otp_exp", time.Now().Add(10*time.Minute).Unix())
			if err := s.sendOTP(ctx, st, d.Phone, "Registration code", otp); err != nil {
				slog.Error("send otp", "err", err)
				show("Failed to send verification code")
				return
			}
			s.prender(w, r, 200, "p_register", Page{Title: "Register", Data: d, Flash: s.catalog.T(s.language(), "Verification code sent")})
			return
		}
		ip := "c:" + clientIP(r)
		code := s.sessions.GetString(ctx, "reg_otp")
		if s.tooManyFailures(ip) || code == "" || f("otp_code") != code ||
			s.sessions.GetString(ctx, "reg_otp_phone") != d.Phone || time.Now().Unix() > s.sessions.GetInt64(ctx, "reg_otp_exp") {
			s.recordFailure(ip)
			show("Wrong Verification code")
			return
		}
		s.sessions.Remove(ctx, "reg_otp")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(f("password")), bcryptCost)
	var enc []byte
	if err == nil {
		enc, err = secret.Seal(s.SecretKey, []byte(f("password")))
	}
	var nc db.Customer
	if err == nil {
		// Active with no plan: the customer buys a plan or redeems a voucher afterwards.
		nc, err = s.queries.CreateCustomer(ctx, db.CreateCustomerParams{Username: d.Username, PasswordHash: string(hash), SecretEnc: enc,
			Fullname: d.Username, Phone: d.Phone, ServiceType: "Others", AutoRenewal: 1, Status: "Active"})
	}
	if err != nil {
		s.fail(w, "register", err)
		return
	}
	s.registered(ctx, st, nc)
	if err := s.portalSignIn(r, nc); err != nil {
		s.fail(w, "register", err)
		return
	}
	s.sessions.Put(ctx, "flash", s.catalog.T(s.language(), "Register Success! You can login now"))
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// registered sends the welcome message and, when reg_nofify_admin is on, tells the admin (Telegram).
func (s *Server) registered(ctx context.Context, st map[string]string, c db.Customer) {
	n, err := notify.Load(ctx, s.queries)
	if err != nil {
		slog.Error("register notify", "err", err)
		return
	}
	n.Go("welcome", func(ctx context.Context) error {
		return n.Custom(ctx, c, "welcome_message", "Welcome", map[string]string{"company": st["company_name"], "Username": c.Username, "url": st["app_url"], "Password": "********"})
	})
	if st["reg_nofify_admin"] == "yes" {
		n.Go("telegram", func(ctx context.Context) error {
			return n.Telegram(ctx, fmt.Sprintf("%s - New User Registration\n\nFull Name: %s\nUsername: %s\nEmail: %s\nPhone Number: %s\nAddress: %s",
				st["company_name"], c.Fullname, c.Username, c.Email, c.Phone, c.Address))
		})
	}
}
