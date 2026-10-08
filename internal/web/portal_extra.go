package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/notify"
)

// Portal features that mirror home.php / voucher.php / accounts.php extras.

func (s *Server) portalRoutes2(mux *http.ServeMux) {
	mux.Handle("GET /portal/voucher", s.requireCustomer(s.pVoucherForm))
	mux.Handle("POST /portal/voucher", s.requireCustomer(s.pVoucher))
	mux.Handle("POST /portal/transfer", s.requireCustomer(s.pTransfer))
	mux.Handle("POST /portal/extend/{id}", s.requireCustomer(s.pExtend))
	mux.Handle("POST /portal/contact/{kind}/otp", s.requireCustomer(s.pContactOTP))
	mux.Handle("POST /portal/contact/{kind}/verify", s.requireCustomer(s.pContactVerify))
}

func (s *Server) flashTo(w http.ResponseWriter, r *http.Request, to, msg string) {
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), msg))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---- voucher activation ----

func (s *Server) pVoucherForm(w http.ResponseWriter, r *http.Request) {
	s.prender(w, r, 200, "p_voucher", Page{Title: "Voucher Activation"})
}

// safeRedirect returns u only when it is an absolute http(s) URL.
func safeRedirect(u string) string {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return ""
	}
	return p.String()
}

func (s *Server) pVoucher(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil || s.Billing == nil {
		s.fail(w, "portal voucher", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["disable_voucher"] == "yes" {
		s.prender(w, r, http.StatusForbidden, "p_voucher", Page{Title: "Voucher Activation", Error: "Voucher activation is disabled"})
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	keys := []string{"vip:" + clientIP(r), "vc:" + strconv.FormatInt(customerFrom(r).ID, 10)}
	for _, k := range keys {
		if s.tooManyFailures(k) {
			s.prender(w, r, http.StatusTooManyRequests, "p_voucher", Page{Title: "Voucher Activation", Error: "Too many failed attempts. Try again in 15 minutes."})
			return
		}
	}
	switch err := s.Billing.RedeemVoucher(r.Context(), code, customerFrom(r).ID); {
	case code == "" || errors.Is(err, billing.ErrVoucherInvalid):
		for _, k := range keys {
			s.recordFailure(k)
		}
		s.prender(w, r, 200, "p_voucher", Page{Title: "Voucher Activation", Error: "Voucher Not Valid"})
	case err != nil:
		s.fail(w, "portal voucher", err)
	default:
		to := "/portal/orders"
		if u := safeRedirect(st["voucher_redirect"]); u != "" {
			to = u
		}
		s.flashTo(w, r, to, "Activation Vouchers Successfully")
	}
}

// ---- balance transfer ----

func (s *Server) pTransfer(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.fail(w, "portal transfer", errors.New("billing not configured"))
		return
	}
	amount, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("balance")), 10, 64)
	err := s.Billing.TransferBalance(r.Context(), customerFrom(r).ID, strings.TrimSpace(r.PostFormValue("username")), amount)
	for _, e := range []error{billing.ErrSelfTransfer, billing.ErrTargetNotFound, billing.ErrBelowMinimum, billing.ErrTransferDisabled, billing.ErrInsufficientBalance, billing.ErrInactive} {
		if errors.Is(err, e) {
			msg := s.catalog.T(s.language(), e.Error())
			if e == billing.ErrBelowMinimum {
				if st, _ := s.loadSettings(r.Context()); st["minimum_transfer"] != "" {
					msg += " " + st["minimum_transfer"]
				}
			}
			s.pDash(w, r, 200, msg)
			return
		}
	}
	if err != nil {
		s.fail(w, "portal transfer", err)
		return
	}
	s.flashTo(w, r, "/portal", "Sending balance success")
}

// ---- self extend ----

func (s *Server) pExtend(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.fail(w, "portal extend", errors.New("billing not configured"))
		return
	}
	until, err := s.Billing.ExtendExpired(r.Context(), customerFrom(r).ID, pathID(r))
	for _, e := range []error{billing.ErrExtendDisabled, billing.ErrExtendAlready, billing.ErrNotExpired, billing.ErrPlanNotFound, billing.ErrInactive} {
		if errors.Is(err, e) {
			s.pDash(w, r, 200, s.catalog.T(s.language(), e.Error()))
			return
		}
	}
	if err != nil {
		s.fail(w, "portal extend", err)
		return
	}
	s.flashTo(w, r, "/portal", "Extend until "+until.Format("2006-01-02"))
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
	val := strings.TrimSpace(r.PostFormValue("value"))
	if kind == "phone" {
		val = normPhone(val)
		if len(val) < 10 {
			s.profilePage(w, r, 200, "Invalid phone number format")
			return
		}
		if st["sms_url"] == "" && st["wa_url"] == "" {
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
	otp, err := newOTP()
	var hash []byte
	if err == nil {
		hash, err = bcrypt.GenerateFromPassword([]byte(otp), bcrypt.DefaultCost)
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
