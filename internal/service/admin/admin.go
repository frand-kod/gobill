// Package admin holds the admin account rules: the legacy sha1 login and the create/update of an account.
// Who may manage whom (canManage, assignable) is decided by the caller and passed in.
package admin

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/mail"
	"slices"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// ErrForbidden is returned by Save when the actor may not give the requested role.
var ErrForbidden = errors.New("role not allowed")

type Service struct {
	Q    *db.Queries
	Cost int // bcrypt cost
}

// LegacyLogin checks an admin imported from PHPNuxBill (sha1 in admins.legacy_sha1).
// On a match it rehashes the password to bcrypt but keeps the marker: the admin is sent to the
// change-password page until they pick a new password (which clears it).
func (s *Service) LegacyLogin(ctx context.Context, id int64, password string) bool {
	want, err := s.Q.GetAdminLegacySha1(ctx, id)
	if err != nil || want == "" {
		return false
	}
	sum := sha1.Sum([]byte(password))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) != 1 {
		return false
	}
	if h, err := bcrypt.GenerateFromPassword([]byte(password), s.Cost); err == nil {
		s.Q.RehashAdminPassword(ctx, db.RehashAdminPasswordParams{PasswordHash: string(h), ID: id})
	}
	return true
}

// SaveInput is one create (Target nil) or update. V holds the trimmed form values: username, fullname,
// email, phone, city, role, status, root.
type SaveInput struct {
	ActorID    int64
	ActorRole  string
	Assignable []string // roles the actor may give
	Target     *db.Admin
	V          map[string]string
	Pass       string
	CPass      string
}

// SaveResult: Errs is not empty when nothing was saved. Role and Status are the values the server
// settled on, whatever the form said. Desc is the audit detail.
type SaveResult struct {
	Role, Status string
	Errs         map[string]string
	Desc         string
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Service) Save(ctx context.Context, in SaveInput) (SaveResult, error) {
	t, v, pass := in.Target, in.V, in.Pass
	self := t != nil && t.ID == in.ActorID
	e := map[string]string{}
	res := SaveResult{Errs: e}

	// Role and status: the server decides, never the form, for self and for Agents.
	role, status := v["role"], v["status"]
	switch {
	case self:
		role, status = t.Role, t.Status
		pass = "" // own password goes through /admin/password
	case in.ActorRole == "Agent":
		role = "Sales"
	case !slices.Contains(in.Assignable, role):
		return res, ErrForbidden
	}
	if t == nil {
		status = "Active"
	} else if status != "Active" && status != "Inactive" {
		status = t.Status
	}
	res.Role, res.Status = role, status

	if n := len([]rune(v["username"])); n < 3 || n > 45 {
		e["username"] = "Username should be between 3 to 45 characters"
	}
	if n := len([]rune(v["fullname"])); n < 3 || n > 45 {
		e["fullname"] = "Full Name should be between 3 to 45 characters"
	}
	if v["email"] != "" {
		if a, err := mail.ParseAddress(v["email"]); err != nil || a.Address != v["email"] {
			e["email"] = "Enter a valid email address"
		}
	}
	if pass != "" || t == nil {
		if n := len(pass); n < 10 || n > 72 { // bcrypt reads at most 72 bytes
			e["password"] = "Password should be 10 to 72 characters"
		} else if pass != in.CPass {
			e["cpassword"] = "Passwords does not match"
		}
	}
	root := sql.NullInt64{}
	if role == "Sales" {
		if in.ActorRole == "Agent" {
			root = sql.NullInt64{Int64: in.ActorID, Valid: true}
		} else if v["root"] != "" {
			rid, _ := strconv.ParseInt(v["root"], 10, 64)
			if ag, err := s.Q.GetAdmin(ctx, rid); err != nil || ag.Role != "Agent" {
				e["root"] = "Agent not found"
			} else {
				root = sql.NullInt64{Int64: rid, Valid: true}
			}
		}
	}
	if len(e) > 0 {
		return res, nil
	}
	var hash []byte
	if pass != "" {
		var err error
		if hash, err = bcrypt.GenerateFromPassword([]byte(pass), s.Cost); err != nil {
			return res, err
		}
	}

	if t == nil {
		_, err := s.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: v["username"], Fullname: v["fullname"],
			PasswordHash: string(hash), Role: role, Email: v["email"], Phone: v["phone"], City: v["city"], RootID: root})
		if isUnique(err) {
			e["username"] = "Account already exist"
			return res, nil
		} else if err != nil {
			return res, err
		}
		res.Desc = v["username"] + " (" + role + ")"
		return res, nil
	}

	// Any change that must cut off existing sessions bumps session_version.
	var bump int64
	if pass != "" || role != t.Role || status != t.Status {
		bump = 1
	}
	n, err := s.Q.UpdateAdmin(ctx, db.UpdateAdminParams{Username: v["username"], Fullname: v["fullname"],
		Email: v["email"], Phone: v["phone"], City: v["city"], Role: role, Status: status, RootID: root,
		PasswordHash: string(hash), Bump: bump, ID: t.ID})
	switch {
	case isUnique(err):
		e["username"] = "Account already exist"
		return res, nil
	case err != nil:
		return res, err
	case n == 0:
		e["role"] = "The last active SuperAdmin cannot be demoted or deactivated"
		return res, nil
	}
	res.Desc = v["username"]
	if role != t.Role {
		res.Desc += ": " + t.Role + " -> " + role
	}
	if pass != "" {
		res.Desc += ": password changed"
	}
	return res, nil
}
