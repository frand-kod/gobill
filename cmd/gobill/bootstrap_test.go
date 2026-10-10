package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

func TestBootstrapAdminPasswordFile(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := db.New(conn)
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)

	dir := t.TempDir()
	if err := bootstrapAdmin(t.Context(), q, dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "initial-admin-password.txt")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	pw := strings.TrimSpace(string(b))
	a, err := q.GetAdminByUsername(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(pw)) != nil {
		t.Fatal("file does not hold the admin password")
	}
	if strings.Contains(logs.String(), pw) || !strings.Contains(logs.String(), path) {
		t.Fatalf("log must name the file and not the password: %s", logs.String())
	}
	// a second start with an admin present writes nothing new
	if err := bootstrapAdmin(t.Context(), q, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
