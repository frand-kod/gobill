package settings

// Admin two-factor login: enrollment and disable on the admin's own page, SuperAdmin reset,
// and the route table shared with the login step (login_2fa.go).

import (
	"encoding/base64"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/core"
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

func (s *Handlers) admin2FAForm(w http.ResponseWriter, r *http.Request) {
	s.render2FA(w, r, http.StatusOK, nil)
}

// render2FA shows the page for the admin in the request. codes is set only right after enabling.
func (s *Handlers) render2FA(w http.ResponseWriter, r *http.Request, status int, codes []string) {
	a := core.AdminFrom(r)
	p := totpPage{Enabled: a.TotpEnabled == 1, Codes: codes}
	if !p.Enabled && a.TotpSecretEnc != "" {
		key, err := s.TotpOpen(a.TotpSecretEnc)
		if err != nil {
			s.Fail(w, "open totp secret", err)
			return
		}
		company := s.Brand(r.Context())["company_name"]
		if company == "" {
			company = "gobill"
		}
		png, err := qrcode.Encode(core.TotpURI(company, a.Username, key), qrcode.Medium, 220)
		if err != nil {
			s.Fail(w, "totp qr", err)
			return
		}
		p.Pending = true
		p.Secret = core.TotpB32.EncodeToString(key)
		p.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	}
	s.Render(w, r, status, "admin_2fa", core.Page{Title: "Two-factor login", Data: p})
}

// admin2FASetup stores a new pending secret. It is not active until confirmed with a code.
func (s *Handlers) admin2FASetup(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), core.AdminFrom(r)
	if a.TotpEnabled == 1 {
		http.Redirect(w, r, "/admin/2fa", http.StatusSeeOther)
		return
	}
	enc, err := s.TotpSeal(core.NewTOTPSecret())
	if err != nil {
		s.Fail(w, "seal totp secret", err)
		return
	}
	if err := s.Queries.SetAdminTOTPSecret(ctx, db.SetAdminTOTPSecretParams{TotpSecretEnc: enc, ID: a.ID}); err != nil {
		s.Fail(w, "save totp secret", err)
		return
	}
	http.Redirect(w, r, "/admin/2fa", http.StatusSeeOther)
}

// admin2FAConfirm checks the first code, enables 2FA and shows the recovery codes once.
func (s *Handlers) admin2FAConfirm(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), core.AdminFrom(r)
	if a.TotpEnabled == 1 || a.TotpSecretEnc == "" {
		http.Redirect(w, r, "/admin/2fa", http.StatusSeeOther)
		return
	}
	ip := core.ClientIP(r)
	if s.TooManyFailures(ip) {
		s.FlashTo(w, r, "/admin/2fa", "Too many failed attempts. Try again in 15 minutes.")
		return
	}
	key, err := s.TotpOpen(a.TotpSecretEnc)
	if err != nil {
		s.Fail(w, "open totp secret", err)
		return
	}
	if !s.TotpVerify(a.ID, key, core.CleanCode(r.PostFormValue("code"))) {
		s.RecordFailure(ip)
		s.FlashTo(w, r, "/admin/2fa", "Invalid code")
		return
	}
	codes := core.NewRecoveryCodes()
	if err := s.Queries.DeleteRecoveryCodes(ctx, a.ID); err != nil {
		s.Fail(w, "clear recovery codes", err)
		return
	}
	for _, c := range codes {
		h, err := bcrypt.GenerateFromPassword([]byte(strings.ReplaceAll(c, "-", "")), core.BcryptCost)
		if err != nil {
			s.Fail(w, "hash recovery code", err)
			return
		}
		if err := s.Queries.CreateRecoveryCode(ctx, db.CreateRecoveryCodeParams{AdminID: a.ID, CodeHash: string(h)}); err != nil {
			s.Fail(w, "save recovery code", err)
			return
		}
	}
	if err := s.Queries.EnableAdminTOTP(ctx, a.ID); err != nil {
		s.Fail(w, "enable totp", err)
		return
	}
	s.LogActivity(r, "users.2fa.enable", a.Username)
	a.TotpEnabled = 1 // this request renders the enabled page with the codes
	s.render2FA(w, r, http.StatusOK, codes)
}

// admin2FADisable needs the current password and a valid code.
func (s *Handlers) admin2FADisable(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), core.AdminFrom(r)
	if a.TotpEnabled != 1 {
		http.Redirect(w, r, "/admin/2fa", http.StatusSeeOther)
		return
	}
	ip := core.ClientIP(r)
	if s.TooManyFailures(ip) {
		s.FlashTo(w, r, "/admin/2fa", "Too many failed attempts. Try again in 15 minutes.")
		return
	}
	key, err := s.TotpOpen(a.TotpSecretEnc)
	if err != nil {
		s.Fail(w, "open totp secret", err)
		return
	}
	pwOK := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(r.PostFormValue("current"))) == nil
	if !pwOK || !s.TotpVerify(a.ID, key, core.CleanCode(r.PostFormValue("code"))) {
		s.RecordFailure(ip)
		s.FlashTo(w, r, "/admin/2fa", "Incorrect password or code")
		return
	}
	if err := s.Queries.ClearAdminTOTP(ctx, a.ID); err != nil {
		s.Fail(w, "clear totp", err)
		return
	}
	if err := s.Queries.DeleteRecoveryCodes(ctx, a.ID); err != nil {
		s.Fail(w, "clear recovery codes", err)
		return
	}
	s.Done(w, r, "/admin/2fa", "Two-factor login turned off", "users.2fa.disable", a.Username)
}

// adminTOTPReset is the SuperAdmin escape hatch for a lost authenticator and recovery codes.
func (s *Handlers) adminTOTPReset(w http.ResponseWriter, r *http.Request) {
	t, ok := s.loadManaged(w, r)
	if !ok {
		return
	}
	if err := s.Queries.ClearAdminTOTP(r.Context(), t.ID); err != nil {
		s.Fail(w, "clear totp", err)
		return
	}
	if err := s.Queries.DeleteRecoveryCodes(r.Context(), t.ID); err != nil {
		s.Fail(w, "clear recovery codes", err)
		return
	}
	s.Done(w, r, "/admin/users/"+strconv.FormatInt(t.ID, 10)+"/edit", "Two-factor login reset", "users.2fa.reset", t.Username)
}
