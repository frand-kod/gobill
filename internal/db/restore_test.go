package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPendingRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nuxbill.db")
	for p, body := range map[string]string{path: "old", path + "-wal": "oldwal", path + "-shm": "oldshm", path + ".restore": "new"} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := ApplyPendingRestore(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("db content %q, want restored file", got)
	}
	for _, p := range []string{path + "-wal", path + "-shm", path + ".restore"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still there: %v", p, err)
		}
	}
	moved, _ := filepath.Glob(path + ".pre-restore-*")
	if len(moved) != 2 { // the db and its -wal, next to the live db
		t.Fatalf("want the old db and wal moved aside, got %v", moved)
	}
	var oldDB, oldWAL string
	for _, m := range moved {
		if strings.HasSuffix(m, "-wal") {
			oldWAL = m
		} else {
			oldDB = m
		}
	}
	if got, _ := os.ReadFile(oldDB); string(got) != "old" {
		t.Fatalf("moved db content %q", got)
	}
	if got, _ := os.ReadFile(oldWAL); string(got) != "oldwal" {
		t.Fatalf("moved wal content %q", got)
	}
}

// Two restores in the same second must not overwrite each other's moved-aside database.
func TestRestoreBackupNamesDoNotCollide(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nuxbill.db")
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(path, []byte("live"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".restore", []byte("backup"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ApplyPendingRestore(path); err != nil {
			t.Fatal(err)
		}
	}
	if moved, _ := filepath.Glob(path + ".pre-restore-*"); len(moved) != 2 {
		t.Fatalf("want 2 distinct moved-aside databases, got %v", moved)
	}
}

func TestApplyPendingRestoreNoStagedFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nuxbill.db")
	if err := os.WriteFile(path, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingRestore(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "live" {
		t.Fatalf("db changed without a staged file: %q", got)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("wal touched without a staged file: %v", err)
	}
}
