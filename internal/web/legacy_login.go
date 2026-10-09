package web

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// legacyLogin checks an admin imported from PHPNuxBill (sha1 in admins.legacy_sha1).
// On a match it rehashes the password to bcrypt and clears the marker, so it works once.
func (s *Server) legacyLogin(ctx context.Context, id int64, password string) bool {
	var want string
	if s.conn.QueryRowContext(ctx, "SELECT legacy_sha1 FROM admins WHERE id = ?", id).Scan(&want) != nil || want == "" {
		return false
	}
	sum := sha1.Sum([]byte(password))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) != 1 {
		return false
	}
	if h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost); err == nil {
		s.conn.ExecContext(ctx, "UPDATE admins SET password_hash = ?, legacy_sha1 = '' WHERE id = ?", string(h), id)
	}
	return true
}
