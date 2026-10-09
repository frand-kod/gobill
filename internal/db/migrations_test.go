package db

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Released migrations are immutable. migrations.sum holds the sha256 of every
// file in migrations/ (format: sha256sum output, basename). Schema changes go
// in a new numbered file, and its line is added to migrations.sum.
func TestMigrationsFrozen(t *testing.T) {
	raw, err := os.ReadFile("migrations.sum")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("migrations.sum: bad line %q", line)
		}
		want[f[1]] = f[0]
	}
	files, err := filepath.Glob("migrations/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(p)
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		switch w, ok := want[name]; {
		case !ok:
			t.Errorf("%s is not in migrations.sum: add its sha256 line (new migrations need one)", name)
		case got != w:
			t.Errorf("%s changed after release (sha256 %s, want %s). Revert it and put the schema change in a new numbered migration", name, got, w)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("%s is in migrations.sum but missing on disk; released migrations are never deleted or renamed", name)
	}
}
