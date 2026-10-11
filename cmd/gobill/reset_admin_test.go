package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

func TestResetAdminPassword(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	q := db.New(conn)
	oldHash, _ := bcrypt.GenerateFromPassword([]byte("old-secret"), bcrypt.MinCost)
	a, err := q.CreateAdmin(ctx, db.CreateAdminParams{Username: "boss", Fullname: "Boss", PasswordHash: string(oldHash), Role: "SuperAdmin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SetAdminTOTPSecret(ctx, db.SetAdminTOTPSecretParams{TotpSecretEnc: "enc", ID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := q.EnableAdminTOTP(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateRecoveryCode(ctx, db.CreateRecoveryCodeParams{AdminID: a.ID, CodeHash: "h"}); err != nil {
		t.Fatal(err)
	}

	// the password is printed once; the password, session_version and 2FA are the only changes
	var out bytes.Buffer
	if err := resetAdminPassword(ctx, q, "boss", false, &out); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(out.String())
	pw, ok := strings.CutPrefix(line, "New password for boss: ")
	if !ok || strings.Count(out.String(), "\n") != 1 || len(pw) != 16 {
		t.Fatalf("output %q", out.String())
	}
	after, _ := q.GetAdmin(ctx, a.ID)
	if bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte(pw)) != nil {
		t.Error("new password does not work")
	}
	if bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte("old-secret")) == nil {
		t.Error("old password still works")
	}
	if after.SessionVersion != a.SessionVersion+1 {
		t.Errorf("session_version %d -> %d, want +1", a.SessionVersion, after.SessionVersion)
	}
	if after.TotpEnabled != 1 {
		t.Error("2FA was turned off without --disable-2fa")
	}

	// --disable-2fa: 2FA off, secret and recovery codes gone
	out.Reset()
	if err := resetAdminPassword(ctx, q, "boss", true, &out); err != nil {
		t.Fatal(err)
	}
	after, _ = q.GetAdmin(ctx, a.ID)
	codes, _ := q.ListUnusedRecoveryCodes(ctx, a.ID)
	if after.TotpEnabled != 0 || after.TotpSecretEnc != "" || len(codes) != 0 {
		t.Errorf("2FA not cleared: enabled=%d secret=%q codes=%d", after.TotpEnabled, after.TotpSecretEnc, len(codes))
	}
	if after.SessionVersion != a.SessionVersion+2 {
		t.Errorf("session_version after second reset: %d", after.SessionVersion)
	}

	// unknown admin: an error, nothing printed
	out.Reset()
	if err := resetAdminPassword(ctx, q, "nobody", false, &out); err == nil || out.Len() != 0 {
		t.Errorf("unknown admin: err=%v out=%q", err, out.String())
	}

	// argument parsing: the flag works before or after the username
	for _, args := range [][]string{{"boss", "--disable-2fa"}, {"--disable-2fa", "boss"}} {
		if u, d, err := parseResetArgs(args); err != nil || u != "boss" || !d {
			t.Errorf("%v: %q %v %v", args, u, d, err)
		}
	}
	for _, args := range [][]string{{}, {"--disable-2fa"}, {"a", "b"}, {"boss", "--force"}} {
		if _, _, err := parseResetArgs(args); err == nil {
			t.Errorf("%v: want usage error", args)
		}
	}
}
