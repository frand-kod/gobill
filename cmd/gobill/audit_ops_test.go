package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A failed restore must leave the live database untouched.
func TestAuditOps4_RestoreDBFailureKeepsLiveDB(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "gobill.db")
	if err := os.WriteFile(live, []byte("live-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreDB(dir, live); err == nil { // a directory: Open works, Read fails mid-copy
		t.Fatal("expected restore error")
	}
	if b, _ := os.ReadFile(live); string(b) != "live-data" {
		t.Fatalf("failed restore destroyed the live DB: %q", b)
	}
}
