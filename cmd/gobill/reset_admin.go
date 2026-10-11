package main

// `gobill reset-admin-password <username> [--disable-2fa]`: the way back in for an admin who lost the
// password. Run it on the server with the same GOBILL_DB as the service. The new password is printed
// to stdout once and never logged. The admin's sessions end (session_version); --disable-2fa also
// turns off two-factor login and deletes the recovery codes.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

const resetUsage = "usage: gobill reset-admin-password <username> [--disable-2fa]"

func runResetAdminPassword(args []string) error {
	user, disable, err := parseResetArgs(args)
	if err != nil {
		return err
	}
	dbPath := env("GOBILL_DB", "./gobill.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("database %s: %w (set GOBILL_DB to the file the service uses)", dbPath, err)
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		return err
	}
	return resetAdminPassword(context.Background(), db.New(conn), user, disable, os.Stdout)
}

// parseResetArgs takes the username and the optional flag, in either order.
func parseResetArgs(args []string) (user string, disable bool, err error) {
	for _, a := range args {
		switch {
		case a == "--disable-2fa" || a == "-disable-2fa":
			disable = true
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("unknown flag %s; %s", a, resetUsage)
		case user != "":
			return "", false, errors.New(resetUsage)
		default:
			user = a
		}
	}
	if user == "" {
		return "", false, errors.New(resetUsage)
	}
	return user, disable, nil
}

// resetAdminPassword sets a random password for the admin and writes it to out, once. Every database
// change happens before the password is written, so a failure never shows a password that was not set.
func resetAdminPassword(ctx context.Context, q *db.Queries, username string, disable2FA bool, out io.Writer) error {
	a, err := q.GetAdminByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no admin named %q", username)
	} else if err != nil {
		return err
	}
	pw := secret.RandomPassword(16)
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := q.SetAdminPassword(ctx, db.SetAdminPasswordParams{PasswordHash: string(hash), ID: a.ID}); err != nil {
		return err
	}
	desc := a.Username
	if disable2FA {
		if err := q.ClearAdminTOTP(ctx, a.ID); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, a.ID); err != nil {
			return err
		}
		desc += " (2FA off)"
	}
	// the description names the admin, never the password
	if err := q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "system", Action: "users.password.cli", Description: desc}); err != nil {
		slog.Warn("reset-admin-password: activity log", "err", err)
	}
	_, err = fmt.Fprintf(out, "New password for %s: %s\n", a.Username, pw)
	return err
}
