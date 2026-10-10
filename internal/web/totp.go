package web

// TOTP (RFC 6238, SHA-1, 6 digits, 30 s) and recovery codes for admin two-factor login.
// Secrets are sealed with the app key (internal/secret); the stdlib does the rest.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/secret"
	"golang.org/x/crypto/bcrypt"
)

const (
	totpPeriod = 30 // seconds per time step
	totpSkew   = 1  // steps accepted either side of now
)

// totpB32 is the base32 text authenticator apps expect (no padding). It is also the recovery code alphabet.
var totpB32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// totpCode returns the 6-digit code for one time step (RFC 4226 HOTP over HMAC-SHA-1).
func totpCode(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	o := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[o:o+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1000000)
}

// newTOTPSecret returns 20 random bytes, the key size authenticator apps use.
func newTOTPSecret() []byte {
	b := make([]byte, 20)
	rand.Read(b)
	return b
}

// totpURI is the otpauth:// URI the QR code carries.
func totpURI(company, username string, key []byte) string {
	q := url.Values{"secret": {totpB32.EncodeToString(key)}, "issuer": {company}}
	return "otpauth://totp/" + url.PathEscape(company) + ":" + url.PathEscape(username) + "?" + q.Encode()
}

// totpVerify checks code against key for the current time (+-totpSkew steps). A step is accepted
// once per admin: a code that was already used, or an older step, is rejected.
// ponytail: last step is kept in memory, so a restart reopens the replay window (max ~90 s); persist if needed.
func (s *Server) totpVerify(adminID int64, key []byte, code string) bool {
	now := time.Now().Unix() / totpPeriod
	for step := now - totpSkew; step <= now+totpSkew; step++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(key, step)), []byte(code)) != 1 {
			continue
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.totpLast == nil {
			s.totpLast = map[int64]int64{}
		}
		if step <= s.totpLast[adminID] {
			return false
		}
		s.totpLast[adminID] = step
		return true
	}
	return false
}

// totpSeal returns the form stored in admins.totp_secret_enc.
func (s *Server) totpSeal(key []byte) (string, error) {
	sealed, err := secret.Seal(s.SecretKey, key)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// totpOpen reverses totpSeal.
func (s *Server) totpOpen(enc string) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, err
	}
	return secret.Open(s.SecretKey, sealed)
}

// newRecoveryCodes returns 8 one-time codes shaped XXXX-XXXX (40 random bits each).
func newRecoveryCodes() []string {
	out := make([]string, 8)
	for i := range out {
		b := make([]byte, 5)
		rand.Read(b)
		c := totpB32.EncodeToString(b)
		out[i] = c[:4] + "-" + c[4:]
	}
	return out
}

// cleanCode drops spaces and dashes and upper-cases, so typed codes match the stored form.
func cleanCode(in string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(in)))
}

// useRecoveryCode spends the unused recovery code that matches code. Each code works once.
func (s *Server) useRecoveryCode(r *http.Request, adminID int64, code string) bool {
	code = cleanCode(code)
	if len(code) != 8 {
		return false
	}
	rows, err := s.queries.ListUnusedRecoveryCodes(r.Context(), adminID)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if bcrypt.CompareHashAndPassword([]byte(row.CodeHash), []byte(code)) != nil {
			continue
		}
		n, err := s.queries.UseRecoveryCode(r.Context(), row.ID)
		return err == nil && n == 1
	}
	return false
}
