package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --force takes a pre-import snapshot of the target; a dry run does not.
func TestImportForceSnapshots(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gobill.db")
	src := filepath.Join(dir, "src.json")
	if err := os.WriteFile(src, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--json", src, "--db", dbPath, "--timezone", "UTC"}
	snaps := func() []string {
		m, _ := filepath.Glob(filepath.Join(dir, "backup", "*-pre-import.db"))
		return m
	}
	if err := runImport(append(args, "--dry-run", "--force")); err != nil {
		t.Fatal(err)
	}
	if n := len(snaps()); n != 0 {
		t.Fatalf("dry run wrote %d snapshots", n)
	}
	if err := runImport(append(args, "--force")); err != nil {
		t.Fatal(err)
	}
	if n := len(snaps()); n != 1 {
		t.Fatalf("forced import wrote %d snapshots, want 1", n)
	}
}
