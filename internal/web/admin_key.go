package web

// The encryption key (gobill.db.key or GOBILL_SECRET_KEY) is never copied to the backup mirror, so the
// SuperAdmin is reminded to keep a copy elsewhere. The page shows the key fingerprint, marks the key as
// saved, and downloads it after the password (and 2FA code) is confirmed.

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"golang.org/x/crypto/bcrypt"
)

const keyPage = "/admin/settings/system/key"

// keyFingerprint is the first 8 hex characters of the SHA-256 of the key.
func keyFingerprint(key []byte) string {
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:4])
}

// keyWarning is the SuperAdmin warning shown until the key is marked saved for the current
// fingerprint. A new key (new fingerprint) shows it again. Empty for other roles.
func (s *Server) keyWarning(r *http.Request) string {
	if a := adminFrom(r); a == nil || a.Role != "SuperAdmin" {
		return ""
	}
	st, err := s.loadSettings(r.Context())
	fp := keyFingerprint(s.SecretKey)
	if err != nil || st["key_saved_fp"] == fp {
		return ""
	}
	return s.catalog.T(s.language(), "The encryption key is not saved outside this server and the backup mirror does not copy it. Without it a restored backup cannot open router passwords and API keys. Open Settings > System & Security > Encryption key. Fingerprint: ") + fp
}

type keyView struct {
	Fingerprint string
	Saved       bool
	TOTP        bool
}

func (s *Server) keyForm(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "load settings", err)
		return
	}
	fp := keyFingerprint(s.SecretKey)
	s.render(w, r, http.StatusOK, "key", Page{Title: "Encryption key", Flash: s.sessions.PopString(r.Context(), "flash"),
		Data: keyView{Fingerprint: fp, Saved: st["key_saved_fp"] == fp, TOTP: adminFrom(r).TotpEnabled == 1}})
}

func (s *Server) keySaved(w http.ResponseWriter, r *http.Request) {
	fp := keyFingerprint(s.SecretKey)
	if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: "key_saved_fp", Value: fp}); err != nil {
		s.fail(w, "save key flag", err)
		return
	}
	s.done(w, r, keyPage, "Marked as saved", "settings.key.saved", fp)
}

// keyDownload sends the key as a file after the current password and, with 2FA on, a valid code.
func (s *Server) keyDownload(w http.ResponseWriter, r *http.Request) {
	a, ip := adminFrom(r), clientIP(r)
	if s.tooManyFailures(ip) {
		s.flashTo(w, r, keyPage, "Too many failed attempts. Try again in 15 minutes.")
		return
	}
	ok := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(r.PostFormValue("current"))) == nil
	if a.TotpEnabled == 1 {
		k, err := s.totpOpen(a.TotpSecretEnc)
		if err != nil {
			s.fail(w, "open totp secret", err)
			return
		}
		ok = s.totpVerify(a.ID, k, cleanCode(r.PostFormValue("code"))) && ok
	}
	if !ok {
		s.recordFailure(ip)
		s.flashTo(w, r, keyPage, "Incorrect password or code")
		return
	}
	s.logActivity(r, "settings.key.download", keyFingerprint(s.SecretKey))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="gobill.db.key"`)
	w.Write([]byte(hex.EncodeToString(s.SecretKey) + "\n"))
}
