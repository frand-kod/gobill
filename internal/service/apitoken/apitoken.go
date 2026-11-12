// Package apitoken issues and checks the bearer tokens of /api/v1.
// Only the sha256 of a token is stored; the plaintext is returned once by Create.
package apitoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

const prefix = "gbt_"

// ErrInvalid is returned for an unknown, revoked or expired token and for an inactive admin.
var ErrInvalid = errors.New("invalid api token")

type Service struct {
	Q   *db.Queries
	Now func() time.Time // nil = wall clock
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func hash(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// Create makes a token for the admin. ttl 0 = never expires. The plaintext is returned only here.
func (s *Service) Create(ctx context.Context, adminID int64, name string, ttl time.Duration) (string, db.ApiToken, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", db.ApiToken{}, err
	}
	plain := prefix + base64.RawURLEncoding.EncodeToString(b)
	var exp int64
	if ttl > 0 {
		exp = s.now().Add(ttl).Unix()
	}
	t, err := s.Q.CreateApiToken(ctx, db.CreateApiTokenParams{AdminID: adminID, Name: name, TokenHash: hash(plain), ExpiresAt: exp})
	return plain, t, err
}

// Revoke stops a token for good. A token that is already revoked or missing returns sql.ErrNoRows.
func (s *Service) Revoke(ctx context.Context, id int64) error {
	n, err := s.Q.RevokeApiToken(ctx, id)
	if err == nil && n == 0 {
		err = sql.ErrNoRows
	}
	return err
}

// List returns the tokens of one admin (hashes only, never the plaintext).
func (s *Service) List(ctx context.Context, adminID int64) ([]db.ApiToken, error) {
	return s.Q.ListApiTokensByAdmin(ctx, adminID)
}

// Verify returns the token row and its admin. The admin's role is read fresh on every call.
func (s *Service) Verify(ctx context.Context, plain string) (db.ApiToken, db.Admin, error) {
	if !strings.HasPrefix(plain, prefix) || len(plain) > 128 {
		return db.ApiToken{}, db.Admin{}, ErrInvalid
	}
	// the lookup is by the hash of the token, so timing reveals nothing about the token itself
	t, err := s.Q.GetApiTokenByHash(ctx, hash(plain))
	if err == sql.ErrNoRows {
		return t, db.Admin{}, ErrInvalid
	}
	if err != nil {
		return t, db.Admin{}, err
	}
	now := s.now().Unix()
	if t.RevokedAt.Valid || (t.ExpiresAt > 0 && t.ExpiresAt <= now) {
		return t, db.Admin{}, ErrInvalid
	}
	a, err := s.Q.GetAdmin(ctx, t.AdminID)
	if err == sql.ErrNoRows || (err == nil && a.Status != "Active") {
		return t, a, ErrInvalid
	}
	if err != nil {
		return t, a, err
	}
	// at most one write a minute per token
	if !t.LastUsedAt.Valid || t.LastUsedAt.Int64 < now-60 {
		_ = s.Q.TouchApiToken(ctx, t.ID)
	}
	return t, a, nil
}
