package web

// Customer self-registration with optional OTP verification.

import (
	"crypto/rand"
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
	Username, Fullname, Email, Address, Phone string
	OTP                                       bool // OTP field shown
	Fname, MEmail, MAddress                   bool // mandatory fields (man_fields_*)
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
	d.Fname, d.MEmail, d.MAddress = st["man_fields_fname"] == "yes", st["man_fields_email"] == "yes", st["man_fields_address"] == "yes"
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
	d := regFlags(regData{Username: strings.TrimSpace(f("username")), Fullname: strings.TrimSpace(f("fullname")),
		Email: strings.TrimSpace(f("email")), Address: strings.TrimSpace(f("address")), Phone: strings.TrimSpace(f("phone_number"))}, st)
	if !d.OTP {
		d.Phone = d.Username // old PHP: the username doubles as the phone number
	}
	show := func(msg string) {
		s.prender(w, r, 200, "p_register", Page{Title: "Register", Data: d, Error: msg})
	}
	if l := len(d.Username); l < 3 || l > 55 {
		show("Username should be between 3 to 55 characters")
		return
	}
	if st["registration_username"] == "phone" && (len(normPhone(d.Username)) < 6 || normPhone(d.Username) != d.Username) {
		show("Phone Number is required")
		return
	}
	if st["registration_username"] == "email" && !strings.Contains(d.Username, "@") {
		show("Email is not Valid")
		return
	}
	if d.Fname && (len(d.Fullname) < 3 || len(d.Fullname) > 25) {
		show("Full Name should be between 3 to 25 characters")
		return
	}
	if d.MEmail && !strings.Contains(d.Email, "@") {
		show("Email is not Valid")
		return
	}
	if d.MAddress && d.Address == "" {
		show("Home Address is required")
		return
	}
	if l := len(f("password")); l < 3 || l > 35 {
		show("Password should be between 3 to 35 characters")
		return
	}
	if f("password") != f("cpassword") {
		show("Passwords does not match")
		return
	}
	if d.OTP {
		if d.Phone == "" {
			show("Phone Number is required")
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
	if _, err := s.queries.GetCustomerByUsername(ctx, d.Username); err == nil {
		show("Account already exists")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(f("password")), bcryptCost)
	var enc []byte
	if err == nil {
		enc, err = secret.Seal(s.SecretKey, []byte(f("password")))
	}
	var nc db.Customer
	if err == nil {
		nc, err = s.queries.CreateCustomer(ctx, db.CreateCustomerParams{Username: d.Username, PasswordHash: string(hash), SecretEnc: enc,
			Fullname: d.Fullname, Address: d.Address, Phone: d.Phone, Email: d.Email, ServiceType: "Others", AutoRenewal: 1, Status: "Active"})
	}
	if err != nil {
		s.fail(w, "register", err)
		return
	}
	s.registered(ctx, st, nc)
	s.sessions.Put(ctx, "flash", s.catalog.T(s.language(), "Register Success! You can login now"))
	http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
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
