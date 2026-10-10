package web

// Customer profile, password and contact change with OTP.

import (
	"log/slog"
	"net/http"

	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"time"
)

// ---- profile ----

func (s *Server) pProfileForm(w http.ResponseWriter, r *http.Request) {
	s.profilePage(w, r, 200, "")
}

func (s *Server) pProfile(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	fullname := strings.TrimSpace(r.PostFormValue("fullname"))
	if fullname == "" {
		s.profilePage(w, r, 200, "Full Name is required")
		return
	}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal profile", err)
		return
	}
	phone, email := strings.TrimSpace(r.PostFormValue("phone")), strings.TrimSpace(r.PostFormValue("email"))
	if st["allow_phone_otp"] == "yes" { // changed only through the OTP flow
		phone = c.Phone
	}
	if st["allow_email_otp"] == "yes" {
		email = c.Email
	}
	err = s.queries.UpdateCustomer(r.Context(), db.UpdateCustomerParams{
		Fullname: fullname, Address: strings.TrimSpace(r.PostFormValue("address")), Phone: phone, Email: email, Coordinates: c.Coordinates,
		ServiceType: c.ServiceType, PppoeUsername: c.PppoeUsername, PppoeIp: c.PppoeIp, SecretEnc: c.SecretEnc,
		AutoRenewal: c.AutoRenewal, Status: c.Status, BillingDay: c.BillingDay, ID: c.ID})
	if err != nil {
		s.fail(w, "portal profile", err)
		return
	}
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), "Data Saved"))
	http.Redirect(w, r, "/portal/profile", http.StatusSeeOther)
}

func (s *Server) pPassword(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	npass := r.PostFormValue("npass")
	msg := ""
	switch {
	case bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(r.PostFormValue("password"))) != nil:
		msg = "Incorrect current password"
	case len(npass) < minPasswordLen || len(npass) > 35:
		msg = "Password should be between 8 to 35 characters"
	case npass != r.PostFormValue("cnpass"):
		msg = "Passwords does not match"
	}
	if msg != "" {
		s.profilePage(w, r, 200, msg)
		return
	}
	err := s.setPassword(r.Context(), c.ID, npass)
	if err == nil {
		err = s.keepSession(r, c.ID)
	}
	if err == nil {
		s.Billing.SyncCustomer(r.Context(), c.ID, 0) // old app pushes the new password to the router
	}
	if err != nil {
		s.fail(w, "portal password", err)
		return
	}
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), "Password changed successfully"))
	http.Redirect(w, r, "/portal/profile", http.StatusSeeOther)
}

// ---- phone / email change with OTP ----

type contactData struct {
	OTPPhone, OTPEmail bool   // change goes through an OTP (allow_phone_otp / allow_email_otp)
	Pending, Value     string // a code was sent for kind Pending to Value
}

func (s *Server) profilePage(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal profile", err)
		return
	}
	d := contactData{OTPPhone: st["allow_phone_otp"] == "yes", OTPEmail: st["allow_email_otp"] == "yes"}
	if k := s.sessions.GetString(r.Context(), "ct_kind"); k != "" && time.Now().Unix() < s.sessions.GetInt64(r.Context(), "ct_exp") {
		d.Pending, d.Value = k, s.sessions.GetString(r.Context(), "ct_val")
	}
	s.prender(w, r, code, "p_profile", Page{Title: "Profile", Error: errMsg, Data: d})
}

func (s *Server) contactClear(r *http.Request) {
	for _, k := range []string{"ct_kind", "ct_val", "ct_hash", "ct_exp", "ct_tries"} {
		s.sessions.Remove(r.Context(), k)
	}
}

// normPhone keeps digits only (old Lang::phoneFormat strips +, spaces and dashes).
func normPhone(p string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, p)
}

// taken reports whether another customer already uses this phone or email.
func (s *Server) contactTaken(r *http.Request, kind, val string, self int64) (bool, error) {
	rows, err := s.queries.SearchCustomers(r.Context(), db.SearchCustomersParams{Q: val, PageLimit: 50, PageOffset: 0})
	if err != nil {
		return false, err
	}
	for _, c := range rows {
		if c.ID == self {
			continue
		}
		if kind == "phone" && c.Phone == val || kind == "email" && strings.EqualFold(c.Email, val) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) pContactOTP(w http.ResponseWriter, r *http.Request) {
	ctx, kind, c := r.Context(), r.PathValue("kind"), customerFrom(r)
	st, err := s.loadSettings(ctx)
	if err != nil || (kind != "phone" && kind != "email") || st["allow_"+kind+"_otp"] != "yes" {
		http.NotFound(w, r)
		return
	}
	if otpOff(st) {
		s.profilePage(w, r, 200, "Verification code is not available right now")
		return
	}
	val := strings.TrimSpace(r.PostFormValue("value"))
	if kind == "phone" {
		val = normPhone(val)
		if len(val) < 10 {
			s.profilePage(w, r, 200, "Invalid phone number format")
			return
		}
		if !notify.SMSConfigured(st) && !notify.WAConfigured(st) {
			s.profilePage(w, r, 200, "SMS server not Available, Please try again later")
			return
		}
	} else if !strings.Contains(val, "@") || len(val) < 5 {
		s.profilePage(w, r, 200, "Email is not Valid")
		return
	}
	if taken, err := s.contactTaken(r, kind, val, c.ID); err != nil {
		s.fail(w, "portal contact", err)
		return
	} else if taken {
		s.profilePage(w, r, 200, "Already registered by another customer")
		return
	}
	if exp := s.sessions.GetInt64(ctx, "ct_exp"); s.sessions.GetString(ctx, "ct_kind") == kind && time.Now().Unix() < exp {
		s.profilePage(w, r, 200, fmt.Sprintf("Please wait %d seconds before sending another code", exp-time.Now().Unix()))
		return
	}
	if !s.otpAllow(clientIP(r), val) {
		s.profilePage(w, r, 200, "Too many verification code requests, please try again later")
		return
	}
	otp, err := newOTP()
	var hash []byte
	if err == nil {
		hash, err = bcrypt.GenerateFromPassword([]byte(otp), bcryptCost)
	}
	if err != nil {
		s.fail(w, "portal contact otp", err)
		return
	}
	if kind == "phone" {
		err = s.sendOTP(ctx, st, val, "Verification code", otp)
	} else {
		var n *notify.Notifier
		if n, err = notify.Load(ctx, s.queries); err == nil {
			err = n.Email(ctx, val, "["+st["company_name"]+"] "+s.catalog.T(s.language(), "Verification code"), otp)
		}
	}
	if err != nil {
		slog.Error("send contact otp", "err", err)
		s.profilePage(w, r, 200, "Failed to send verification code")
		return
	}
	s.sessions.Put(ctx, "ct_kind", kind)
	s.sessions.Put(ctx, "ct_val", val)
	s.sessions.Put(ctx, "ct_hash", string(hash))
	s.sessions.Put(ctx, "ct_exp", time.Now().Add(otpTTL).Unix())
	s.sessions.Put(ctx, "ct_tries", otpTries)
	s.flashTo(w, r, "/portal/profile", "Verification code sent")
}

func (s *Server) pContactVerify(w http.ResponseWriter, r *http.Request) {
	ctx, kind, c := r.Context(), r.PathValue("kind"), customerFrom(r)
	if st, err := s.loadSettings(ctx); err != nil {
		s.fail(w, "contact verify settings", err)
		return
	} else if otpOff(st) {
		s.profilePage(w, r, 200, "Verification code is not available right now")
		return
	}
	if s.sessions.GetString(ctx, "ct_kind") != kind || time.Now().Unix() > s.sessions.GetInt64(ctx, "ct_exp") {
		s.contactClear(r)
		s.profilePage(w, r, 200, "Verification code expired")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(s.sessions.GetString(ctx, "ct_hash")), []byte(strings.TrimSpace(r.PostFormValue("otp")))) != nil {
		tries := s.sessions.GetInt(ctx, "ct_tries") - 1
		if tries <= 0 {
			s.contactClear(r)
			s.profilePage(w, r, 200, "Too many invalid attempts, please request a new Verification Code")
			return
		}
		s.sessions.Put(ctx, "ct_tries", tries)
		s.profilePage(w, r, 200, "Wrong Verification code")
		return
	}
	val := s.sessions.GetString(ctx, "ct_val")
	s.contactClear(r)
	phone, email := c.Phone, c.Email
	if kind == "phone" {
		phone = val
	} else {
		email = val
	}
	if err := s.queries.UpdateCustomer(ctx, db.UpdateCustomerParams{Fullname: c.Fullname, Address: c.Address, Phone: phone, Email: email,
		ServiceType: c.ServiceType, PppoeUsername: c.PppoeUsername, PppoeIp: c.PppoeIp, SecretEnc: c.SecretEnc,
		AutoRenewal: c.AutoRenewal, Status: c.Status, BillingDay: c.BillingDay, Coordinates: c.Coordinates, ID: c.ID}); err != nil {
		s.fail(w, "portal contact", err)
		return
	}
	s.flashTo(w, r, "/portal/profile", "Data Saved")
}
