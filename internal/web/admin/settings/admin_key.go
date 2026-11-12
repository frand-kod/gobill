package settings

// The encryption key (gobill.db.key or GOBILL_SECRET_KEY) is never copied to the backup mirror, so the
// SuperAdmin is reminded to keep a copy elsewhere. The page shows the key fingerprint, marks the key as
// saved, and downloads it after the password (and 2FA code) is confirmed.

import (
	"encoding/hex"
	"net/http"

	"github.com/frand-kod/gobill/internal/service/restore"
	"github.com/frand-kod/gobill/internal/web/core"

	"github.com/frand-kod/gobill/internal/db"
	"golang.org/x/crypto/bcrypt"
)

const KeyPage = "/admin/settings/system/key"

type keyView struct {
	Fingerprint string
	Saved       bool
	TOTP        bool
}

func (s *Handlers) keyForm(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "load settings", err)
		return
	}
	fp := restore.KeyFingerprint(s.SecretKey)
	s.Render(w, r, http.StatusOK, "key", core.Page{Title: "Encryption key", Flash: s.Sessions.PopString(r.Context(), "flash"),
		Data: keyView{Fingerprint: fp, Saved: st["key_saved_fp"] == fp, TOTP: core.AdminFrom(r).TotpEnabled == 1}})
}

func (s *Handlers) keySaved(w http.ResponseWriter, r *http.Request) {
	fp := restore.KeyFingerprint(s.SecretKey)
	if err := s.Queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: "key_saved_fp", Value: fp}); err != nil {
		s.Fail(w, "save key flag", err)
		return
	}
	s.Done(w, r, KeyPage, "Marked as saved", "settings.key.saved", fp)
}

// stepUp checks the current password and, with 2FA on, a valid code from the form. It returns a
// message to show when the check fails (failures count toward the login throttle), or an error
// when the 2FA secret cannot be opened.
func (s *Handlers) stepUp(r *http.Request) (string, error) {
	a, ip := core.AdminFrom(r), core.ClientIP(r)
	if s.Reserve(ip, core.MaxFailedLogins) < 0 {
		return "Too many failed attempts. Try again in 15 minutes.", nil
	}
	ok := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(r.PostFormValue("current"))) == nil
	if a.TotpEnabled == 1 {
		k, err := s.TotpOpen(a.TotpSecretEnc)
		if err != nil {
			s.Unreserve(ip)
			return "", err
		}
		ok = s.TotpVerify(a.ID, k, core.CleanCode(r.PostFormValue("code"))) && ok
	}
	if !ok {
		return "Incorrect password or code", nil
	}
	s.Unreserve(ip)
	return "", nil
}

// keyDownload sends the key as a file after the current password and, with 2FA on, a valid code.
func (s *Handlers) keyDownload(w http.ResponseWriter, r *http.Request) {
	msg, err := s.stepUp(r)
	if err != nil {
		s.Fail(w, "open totp secret", err)
		return
	}
	if msg != "" {
		s.FlashTo(w, r, KeyPage, msg)
		return
	}
	s.LogActivity(r, "settings.key.download", restore.KeyFingerprint(s.SecretKey))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="gobill.db.key"`)
	w.Write([]byte(hex.EncodeToString(s.SecretKey) + "\n"))
}
