package web

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/notify"
	"github.com/frand-kod/nuxbill-go/internal/payment"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

// Customer portal. The session key "customer_id" is separate from the admin's "admin_id".

type custCtxKey struct{}

func customerFrom(r *http.Request) *db.Customer {
	c, _ := r.Context().Value(custCtxKey{}).(*db.Customer)
	return c
}

func (s *Server) requireCustomer(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := s.sessions.GetInt64(r.Context(), "customer_id")
		c, err := s.queries.GetCustomer(r.Context(), id)
		if id == 0 || err != nil || c.Status == "Banned" || c.Status == "Disabled" || c.SessionVersion != s.sessions.GetInt64(r.Context(), "csv") {
			s.sessions.Remove(r.Context(), "customer_id")
			http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), custCtxKey{}, &c)))
	})
}

func (s *Server) portalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /portal/login", s.pLoginForm)
	mux.HandleFunc("POST /portal/login", s.pLogin)
	mux.HandleFunc("POST /portal/logout", s.pLogout)
	mux.HandleFunc("POST /portal/login/activation", s.pVoucherLogin)
	mux.HandleFunc("GET /portal/register", s.pRegisterForm)
	mux.HandleFunc("POST /portal/register", s.pRegister)
	mux.Handle("GET /portal", s.requireCustomer(s.pDashboard))
	mux.Handle("GET /portal/profile", s.requireCustomer(s.pProfileForm))
	mux.Handle("POST /portal/profile", s.requireCustomer(s.pProfile))
	mux.Handle("POST /portal/password", s.requireCustomer(s.pPassword))
	mux.Handle("GET /portal/orders", s.requireCustomer(s.pOrders))
	mux.Handle("GET /portal/orders/{id}/invoice", s.requireCustomer(s.pInvoice))
	mux.Handle("GET /portal/plans", s.requireCustomer(s.pPlans))
	mux.Handle("POST /portal/plans/{id}/balance", s.requireCustomer(s.pBuyBalance))
	s.inboxRoutes(mux)
	s.portalRoutes2(mux)
	mux.HandleFunc("GET /portal/forgot", s.pForgotForm)
	mux.HandleFunc("POST /portal/forgot", s.pForgotSend)
	mux.HandleFunc("POST /portal/forgot/verify", s.pForgotVerify)
	mux.HandleFunc("POST /portal/forgot/reset", s.pForgotReset)
	mux.HandleFunc("GET /pages/{slug}", s.pagePublic)
}

func (s *Server) prender(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	p.Customer = customerFrom(r)
	if p.Customer != nil {
		p.Unread, _ = s.queries.CountUnreadInbox(r.Context(), p.Customer.ID)
		p.Impersonating = s.sessions.GetInt64(r.Context(), "impersonator") != 0
	}
	if p.Flash == "" {
		p.Flash = s.sessions.PopString(r.Context(), "flash")
	}
	s.render(w, r, status, name, p)
}

// ---- login ----

func (s *Server) pLoginForm(w http.ResponseWriter, r *http.Request) {
	s.prender(w, r, 200, "p_login", Page{Title: "Sign in"})
}

func (s *Server) pLogin(w http.ResponseWriter, r *http.Request) {
	ip := "c:" + clientIP(r)
	if s.tooManyFailures(ip) {
		s.prender(w, r, http.StatusTooManyRequests, "p_login", Page{Title: "Sign in", Error: "Too many failed attempts. Try again in 15 minutes."})
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	c, err := s.queries.GetCustomerByUsername(r.Context(), username)
	hash := s.dummyHash
	if err == nil {
		hash = []byte(c.PasswordHash)
	}
	ok := bcrypt.CompareHashAndPassword(hash, []byte(r.PostFormValue("password"))) == nil
	if err != nil || !ok || c.Status == "Banned" || c.Status == "Disabled" {
		s.recordFailure(ip)
		s.prender(w, r, 200, "p_login", Page{Title: "Sign in", Error: "Invalid Username or Password", Data: username})
		return
	}
	s.clearFailures(ip)
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		s.fail(w, "renew session", err)
		return
	}
	s.sessions.Put(r.Context(), "customer_id", c.ID)
	s.sessions.Put(r.Context(), "csv", c.SessionVersion)
	if err := s.queries.TouchCustomerLogin(r.Context(), c.ID); err != nil {
		slog.Error("touch login", "err", err)
	}
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// keepSession re-stamps the acting session with the customer's bumped session_version.
func (s *Server) keepSession(r *http.Request, id int64) error {
	c, err := s.queries.GetCustomer(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		return err
	}
	s.sessions.Put(r.Context(), "csv", c.SessionVersion)
	return nil
}

func (s *Server) pLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.Remove(r.Context(), "customer_id")
	s.sessions.Remove(r.Context(), "impersonator")
	http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
}

// ---- register ----

// pVoucherLogin is the old login/activation: in "Voucher Only" mode a username plus a voucher code
// creates the customer (password and PPPoE secret = the code) when missing, then redeems the voucher.
// An existing customer keeps their password (old PHP overwrote it with the code).
func (s *Server) pVoucherLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.loadSettings(ctx)
	if err != nil || s.Billing == nil {
		s.fail(w, "voucher login", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["disable_registration"] != "yes" {
		s.flashTo(w, r, "/portal/login", "Registration Disabled")
		return
	}
	ip := "c:" + clientIP(r)
	bad := func() {
		s.recordFailure(ip)
		s.prender(w, r, 200, "p_login", Page{Title: "Sign in", Error: "Voucher Not Valid"})
	}
	code := strings.TrimSpace(r.PostFormValue("voucher"))
	username := strings.Map(func(c rune) rune {
		if c < 128 && (unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("+_.@-", c)) {
			return c
		}
		return -1
	}, r.PostFormValue("username"))
	if s.tooManyFailures(ip) || code == "" || len(username) < 3 || len(username) > 55 {
		bad()
		return
	}
	if v, err := s.queries.GetVoucherByCode(ctx, code); err != nil || v.Status != "unused" {
		bad()
		return
	}
	c, err := s.queries.GetCustomerByUsername(ctx, username)
	if err != nil {
		hash, herr := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		enc, serr := secret.Seal(s.SecretKey, []byte(code))
		if herr != nil || serr != nil {
			s.fail(w, "voucher login", errors.Join(herr, serr))
			return
		}
		phone := ""
		if len(username) < 21 {
			phone = username
		}
		if c, err = s.queries.CreateCustomer(ctx, db.CreateCustomerParams{Username: username, PasswordHash: string(hash),
			Phone: phone, ServiceType: "Others", SecretEnc: enc, AutoRenewal: 1, Status: "Active"}); err != nil {
			s.fail(w, "voucher login", err)
			return
		}
	}
	switch err := s.Billing.RedeemVoucher(ctx, code, c.ID); {
	case errors.Is(err, billing.ErrVoucherInvalid):
		bad()
	case err != nil:
		s.fail(w, "voucher login", err)
	default:
		to := "/portal/login"
		if u := safeRedirect(st["voucher_redirect"]); u != "" {
			to = u
		}
		s.flashTo(w, r, to, "Voucher activation success, now you can login")
	}
}

type regData struct {
	Username, Fullname, Email, Address, Phone string
	OTP                                       bool // OTP field shown
	Fname, MEmail, MAddress                   bool // mandatory fields (man_fields_*)
}

// otpEnabled: old sms_otp_registration, but only when a WA/SMS gateway exists to deliver it.
func otpEnabled(st map[string]string) bool {
	return st["sms_otp_registration"] == "yes" && (st["sms_url"] != "" || st["wa_url"] != "")
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
	hash, err := bcrypt.GenerateFromPassword([]byte(f("password")), bcrypt.DefaultCost)
	var nc db.Customer
	if err == nil {
		nc, err = s.queries.CreateCustomer(ctx, db.CreateCustomerParams{Username: d.Username, PasswordHash: string(hash),
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

func (s *Server) sendOTP(ctx context.Context, st map[string]string, phone, label, otp string) error {
	n, err := notify.Load(ctx, s.queries)
	if err != nil {
		return err
	}
	msg := st["company_name"] + "\n\n" + s.catalog.T(s.language(), label) + "\n" + otp
	typ := st["phone_otp_type"]
	if typ == "whatsapp" || typ == "both" {
		if err := n.WhatsApp(ctx, phone, msg); err != nil {
			return err
		}
	}
	if typ != "whatsapp" {
		return n.SMS(ctx, phone, msg)
	}
	return nil
}

// ---- dashboard, orders ----

type pSubRow struct {
	ID                 int64
	Plan, Type, Status string
	Expires            int64
	CanExtend          bool
}

type pDashData struct {
	Subs     []pSubRow
	Notice   string // the announcement page, plain text
	Transfer bool   // allow_balance_transfer
	Minimum  string
	Voucher  bool
}

func (s *Server) pDashboard(w http.ResponseWriter, r *http.Request) { s.pDash(w, r, 200, "") }

func (s *Server) pDash(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	c := customerFrom(r)
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal dashboard", err)
		return
	}
	subs, err := s.queries.ListSubscriptionsByCustomer(r.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
	if err != nil {
		s.fail(w, "portal dashboard", err)
		return
	}
	d := pDashData{Transfer: st["enable_balance"] != "no" && st["allow_balance_transfer"] == "yes", Minimum: st["minimum_transfer"], Voucher: st["disable_voucher"] != "yes"}
	canExtend := (st["extend_expired"] == "1" || st["extend_expired"] == "yes") && c.Status == "Active"
	for _, sub := range subs {
		p, _ := s.queries.GetPlan(r.Context(), sub.PlanID)
		d.Subs = append(d.Subs, pSubRow{sub.ID, p.Name, sub.Type, sub.Status, sub.ExpiresAt, canExtend && sub.Status != "active"})
	}
	ann, err := s.queries.GetPage(r.Context(), "announcement")
	if err != nil {
		s.fail(w, "portal announcement", err)
		return
	}
	d.Notice = ann.Body
	s.prender(w, r, code, "p_dashboard", Page{Title: "Dashboard", Error: errMsg, Data: d})
}

func (s *Server) pOrders(w http.ResponseWriter, r *http.Request) {
	trx, err := s.queries.ListTransactionsByCustomer(r.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: customerFrom(r).ID, Valid: true}, Limit: 100})
	if err != nil {
		s.fail(w, "portal orders", err)
		return
	}
	s.prender(w, r, 200, "p_orders", Page{Title: "Order History", Data: trx})
}

// pInvoice shows the invoice only for the customer's own transaction.
func (s *Server) pInvoice(w http.ResponseWriter, r *http.Request) {
	t, err := s.queries.GetTransaction(r.Context(), pathID(r))
	if err != nil || !t.CustomerID.Valid || t.CustomerID.Int64 != customerFrom(r).ID {
		http.NotFound(w, r)
		return
	}
	s.invoice(w, r, t)
}

type planRow struct {
	db.Plan
	Bandwidth string // shown when show_bandwidth_plan == yes
}

type plansData struct {
	Plans    []planRow
	Balance  bool
	Channels []payment.Channel // online gateway channels; empty = gateway off
	Custom   bool              // allow_balance_custom with a gateway: custom top-up form
}

func (s *Server) pPlans(w http.ResponseWriter, r *http.Request) {
	s.plansPage(w, r, 200, "")
}

func (s *Server) plansPage(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	c := customerFrom(r)
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "portal plans", err)
		return
	}
	types := []string{"Hotspot", "PPPoE"}
	if c.ServiceType != "Others" {
		types = []string{c.ServiceType}
	}
	var plans []planRow
	for _, t := range types {
		l, err := s.queries.ListEnabledPlansByType(r.Context(), t)
		if err != nil {
			s.fail(w, "portal plans", err)
			return
		}
		for _, p := range l {
			if p.Billing == "prepaid" {
				row := planRow{Plan: p}
				if bw, err := s.queries.GetBandwidth(r.Context(), p.BandwidthID.Int64); st["show_bandwidth_plan"] == "yes" && p.BandwidthID.Valid && err == nil {
					row.Bandwidth = bw.Name
				}
				plans = append(plans, row)
			}
		}
	}
	pd := plansData{Plans: plans, Balance: st["enable_balance"] != "no"}
	pd.Custom = pd.Balance && st["allow_balance_custom"] == "yes"
	if g, err := s.gateway(st); g != nil && err == nil {
		if pd.Channels, err = s.activeChannels(r.Context(), g); err != nil {
			slog.Error("portal plans: channels", "err", err)
		}
	}
	s.prender(w, r, code, "p_plans", Page{Title: "Order Package", Error: errMsg, Data: pd})
}

// couponErr returns the user-facing message of a coupon validation error, or "".
func couponErr(err error) string {
	for _, e := range []error{billing.ErrCouponNotFound, billing.ErrCouponInactive, billing.ErrCouponDate, billing.ErrCouponUsed, billing.ErrCouponMin, billing.ErrCouponTooBig} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return ""
}

// pBuyBalance pays a plan from the customer's balance.
// The online gateway (Tripay) order is pPay in payment.go.
func (s *Server) pBuyBalance(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r)
	st, err := s.loadSettings(r.Context())
	if err != nil || s.Billing == nil {
		s.fail(w, "portal buy", errors.Join(err, errors.New("billing not configured")))
		return
	}
	if st["enable_balance"] == "no" {
		s.plansPage(w, r, 200, "Balance not enabled")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := s.queries.GetPlan(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (p.Enabled != 1 || p.Type == "Balance" || p.Billing != "prepaid") {
		http.NotFound(w, r)
		return
	}
	if err == nil {
		if code := strings.TrimSpace(r.PostFormValue("coupon")); code != "" {
			err = s.Billing.RechargeWithBalanceCoupon(r.Context(), c.ID, p.ID, code)
		} else {
			err = s.Billing.RechargeWithBalance(r.Context(), c.ID, p.ID, 0)
		}
	}
	if m := couponErr(err); m != "" {
		s.plansPage(w, r, 200, s.catalog.T(s.language(), m))
		return
	}
	if errors.Is(err, billing.ErrInsufficientBalance) {
		s.plansPage(w, r, 200, "Insufficient balance")
		return
	}
	if err != nil {
		s.fail(w, "portal buy", err)
		return
	}
	s.sessions.Put(r.Context(), "flash", fmt.Sprintf("%s: %s", s.catalog.T(s.language(), "Package activated"), p.Name))
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

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
	case len(npass) < 3 || len(npass) > 35:
		msg = "Password should be between 3 to 35 characters"
	case npass != r.PostFormValue("cnpass"):
		msg = "Passwords does not match"
	}
	if msg != "" {
		s.profilePage(w, r, 200, msg)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(npass), bcrypt.DefaultCost)
	if err == nil {
		err = s.queries.SetCustomerPassword(r.Context(), db.SetCustomerPasswordParams{PasswordHash: string(hash), ID: c.ID})
	}
	if err == nil {
		err = s.keepSession(r, c.ID)
	}
	if err != nil {
		s.fail(w, "portal password", err)
		return
	}
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), "Password changed successfully"))
	http.Redirect(w, r, "/portal/profile", http.StatusSeeOther)
}
