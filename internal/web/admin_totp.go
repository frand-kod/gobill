package web

// Admin two-factor login: enrollment and disable on the admin's own page, SuperAdmin reset,
// and the route table shared with the login step (login_2fa.go).

import (
	"encoding/base64"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
)

// totpPage is the data of the admin 2FA page.
type totpPage struct {
	Enabled bool
	Pending bool   // secret set up, waiting for the first code
	Secret  string // base32, for typing the key by hand
	QR      template.URL
	Codes   []string // recovery codes, shown once right after enabling
}

func (s *Server) totpRoutes(mux *http.ServeMux, all func(http.Handler) http.Handler) {
	mux.HandleFunc("GET /login/2fa", s.login2FAForm)
	mux.HandleFunc("POST /login/2fa", s.login2FASubmit)
	mux.Handle("GET /admin/2fa", all(http.HandlerFunc(s.admin2FAForm)))
	mux.Handle("POST /admin/2fa/setup", all(http.HandlerFunc(s.admin2FASetup)))
	mux.Handle("POST /admin/2fa/confirm", all(http.HandlerFunc(s.admin2FAConfirm)))
	mux.Handle("POST /admin/2fa/disable", all(http.HandlerFunc(s.admin2FADisable)))
	mux.Handle("POST /admin/users/{id}/2fa/reset", s.requireAdmin("SuperAdmin")(http.HandlerFunc(s.adminTOTPReset)))
}

func (s *Server) admin2FAForm(w http.ResponseWriter, r *http.Request) {
	s.render2FA(w, r, http.StatusOK, nil)
}

// render2FA shows the page for the admin in the request. codes is set only right after enabling.
func (s *Server) render2FA(w http.ResponseWriter, r *http.Request, status int, codes []string) {
	a := adminFrom(r)
	p := totpPage{Enabled: a.TotpEnabled == 1, Codes: codes}
	if !p.Enabled && a.TotpSecretEnc != "" {
		key, err := s.totpOpen(a.TotpSecretEnc)
		if err != nil {
			s.fail(w, "open totp secret", err)
			return
		}
		company := s.brand(r.Context())["company_name"]
		if company == "" {
			company = "gobill"
		}
		png, err := qrcode.Encode(totpURI(company, a.Username, key), qrcode.Medium, 220)
		if err != nil {
			s.fail(w, "totp qr", err)
			return
		}
		p.Pending = true
		p.Secret = totpB32.EncodeToString(key)
		p.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	}
	s.render(w, r, status, "admin_2fa", Page{Title: "Two-factor login", Data: p})
}

// admin2FASetup stores a new pending secret. It is not active until confirmed with a code.
func (s *Server) admin2FASetup(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), adminFrom(r)
	if a.TotpEnabled == 1 {
		http.Redirect(w, r, "/admin/2fa", http.StatusSeeOther)
		return
	}
	enc, err := s.totpSeal(newTOTPSecret())
	if err != nil {
		s.fail(w, "seal totp secret", err)
		return
	}
	if err := s.queries.SetAdminTOTPSecret(ctx, db.SetAdminTOTPSecretParams{TotpSecretEnc: enc, ID: a.ID}); err != nil {
		s.fail(w, "save totp secret", err)
		return
	}
	http.Redirect(w, r, "/admin/2fa", http.StatusSeeOther)
}

// admin2FAConfirm checks the first code, enables 2FA and shows the recovery codes once.
func (s *Server) admin2FAConfirm(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), adminFrom(r)
	if a.TotpEnabled == 1 || a.TotpSecretEnc == "" {
		http.Redirect(w, r, "/admin/2fa", http.StatusSeeOther)
		return
	}
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		s.flashTo(w, r, "/admin/2fa", "Too many failed attempts. Try again in 15 minutes.")
		return
	}
	key, err := s.totpOpen(a.TotpSecretEnc)
	if err != nil {
		s.fail(w, "open totp secret", err)
		return
	}
	if !s.totpVerify(a.ID, key, cleanCode(r.PostFormValue("code"))) {
		s.recordFailure(ip)
		s.flashTo(w, r, "/admin/2fa", "Invalid code")
		return
	}
	codes := newRecoveryCodes()
	if err := s.queries.DeleteRecoveryCodes(ctx, a.ID); err != nil {
		s.fail(w, "clear recovery codes", err)
		return
	}
	for _, c := range codes {
		h, err := bcrypt.GenerateFromPassword([]byte(strings.ReplaceAll(c, "-", "")), bcryptCost)
		if err != nil {
			s.fail(w, "hash recovery code", err)
			return
		}
		if err := s.queries.CreateRecoveryCode(ctx, db.CreateRecoveryCodeParams{AdminID: a.ID, CodeHash: string(h)}); err != nil {
			s.fail(w, "save recovery code", err)
			return
		}
	}
	if err := s.queries.EnableAdminTOTP(ctx, a.ID); err != nil {
		s.fail(w, "enable totp", err)
		return
	}
	s.logActivity(r, "users.2fa.enable", a.Username)
	a.TotpEnabled = 1 // this request renders the enabled page with the codes
	s.render2FA(w, r, http.StatusOK, codes)
}

// admin2FADisable needs the current password and a valid code.
func (s *Server) admin2FADisable(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), adminFrom(r)
	if a.TotpEnabled != 1 {
		http.Redirect(w, r, "/admin/2fa", http.StatusSeeOther)
		return
	}
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		s.flashTo(w, r, "/admin/2fa", "Too many failed attempts. Try again in 15 minutes.")
		return
	}
	key, err := s.totpOpen(a.TotpSecretEnc)
	if err != nil {
		s.fail(w, "open totp secret", err)
		return
	}
	pwOK := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(r.PostFormValue("current"))) == nil
	if !pwOK || !s.totpVerify(a.ID, key, cleanCode(r.PostFormValue("code"))) {
		s.recordFailure(ip)
		s.flashTo(w, r, "/admin/2fa", "Incorrect password or code")
		return
	}
	if err := s.queries.ClearAdminTOTP(ctx, a.ID); err != nil {
		s.fail(w, "clear totp", err)
		return
	}
	if err := s.queries.DeleteRecoveryCodes(ctx, a.ID); err != nil {
		s.fail(w, "clear recovery codes", err)
		return
	}
	s.done(w, r, "/admin/2fa", "Two-factor login turned off", "users.2fa.disable", a.Username)
}

// adminTOTPReset is the SuperAdmin escape hatch for a lost authenticator and recovery codes.
func (s *Server) adminTOTPReset(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadManaged(w, r)
	if !ok {
		return
	}
	if err := s.queries.ClearAdminTOTP(r.Context(), t.ID); err != nil {
		s.fail(w, "clear totp", err)
		return
	}
	if err := s.queries.DeleteRecoveryCodes(r.Context(), t.ID); err != nil {
		s.fail(w, "clear recovery codes", err)
		return
	}
	s.done(w, r, "/admin/users/"+strconv.FormatInt(t.ID, 10)+"/edit", "Two-factor login reset", "users.2fa.reset", t.Username)
}
