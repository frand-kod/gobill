package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/update"
)

// fakeGitHub serves one release at /latest and /tags/<tag>, with the new binary and SHA256SUMS.
func fakeGitHub(t *testing.T, tag string, bin []byte, sums string, withSums bool) *httptest.Server {
	t.Helper()
	name, err := update.AssetName(runtime.GOARCH, buildGOARM())
	if err != nil {
		t.Skipf("no release asset for this platform: %v", err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/dl/bin":
			w.Write(bin)
		case r.URL.Path == "/dl/sums":
			w.Write([]byte(sums))
		case r.URL.Path == "/latest" || strings.HasPrefix(r.URL.Path, "/tags/"):
			rel := update.Release{Tag: tag, URL: "https://example.test/" + tag, Notes: "first line\nsecond line",
				Assets: []update.Asset{{Name: name, URL: srv.URL + "/dl/bin"}}}
			if withSums {
				rel.Assets = append(rel.Assets, update.Asset{Name: "SHA256SUMS", URL: srv.URL + "/dl/sums"})
			}
			json.NewEncoder(w).Encode(rel)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sumsFor(bin []byte) string {
	h := sha256.Sum256(bin)
	name, _ := update.AssetName(runtime.GOARCH, buildGOARM())
	return hex.EncodeToString(h[:]) + "  " + name + "\n"
}

// testUpdater builds an updater over a temp install: a binary "old", a migrated database and a
// fake systemctl that records its calls.
type testUpdater struct {
	*updater
	calls  *[]string
	health *error
}

func newTestUpdater(t *testing.T, api, input string) testUpdater {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gobill.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	bin := filepath.Join(dir, "gobill")
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var calls []string
	var health error
	u := &updater{
		bin:         bin,
		dbPath:      dbPath,
		backupDir:   filepath.Join(dir, "backup"),
		infoPath:    filepath.Join(dir, "last-update.json"),
		healthURL:   "http://127.0.0.1:1/health",
		apiURL:      api,
		client:      &http.Client{Timeout: 5 * time.Second},
		in:          bufio.NewReader(strings.NewReader(input)),
		out:         &bytes.Buffer{},
		systemctl:   func(action string) error { calls = append(calls, action); return nil },
		waitHealthy: func(string, time.Duration) error { return health },
	}
	return testUpdater{updater: u, calls: &calls, health: &health}
}

func TestUpdateAlreadyUpToDate(t *testing.T) {
	srv := fakeGitHub(t, "v0.2.0", []byte("new"), sumsFor([]byte("new")), true)
	tu := newTestUpdater(t, srv.URL, "")
	if err := tu.update(t.Context(), "v0.2.0", "", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tu.out.(*bytes.Buffer).String(), "already up to date") || len(*tu.calls) != 0 {
		t.Fatalf("output %q, systemctl %v", tu.out, *tu.calls)
	}
}

func TestUpdateDevBuildNeedsVersion(t *testing.T) {
	srv := fakeGitHub(t, "v0.2.0", []byte("new"), sumsFor([]byte("new")), true)
	tu := newTestUpdater(t, srv.URL, "")
	if err := tu.update(t.Context(), "v0.1.6-3-gabc", "", true); err == nil || !strings.Contains(err.Error(), "--version") {
		t.Fatalf("dev build without --version: %v", err)
	}
}

func TestUpdateRefusesBadChecksum(t *testing.T) {
	srv := fakeGitHub(t, "v0.2.0", []byte("new"), sumsFor([]byte("other")), true)
	tu := newTestUpdater(t, srv.URL, "")
	if err := tu.update(t.Context(), "v0.1.6", "", true); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("bad checksum accepted: %v", err)
	}
	if b, _ := os.ReadFile(tu.bin); string(b) != "old" || len(*tu.calls) != 0 {
		t.Fatalf("binary changed to %q, systemctl %v", b, *tu.calls)
	}
	if _, err := os.Stat(tu.backupDir); err == nil {
		t.Fatal("database backup taken before the checksum passed")
	}
}

func TestUpdateRefusesMissingChecksumFile(t *testing.T) {
	srv := fakeGitHub(t, "v0.2.0", []byte("new"), "", false)
	tu := newTestUpdater(t, srv.URL, "")
	if err := tu.update(t.Context(), "v0.1.6", "", true); err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
		t.Fatalf("release without SHA256SUMS accepted: %v", err)
	}
}

func TestUpdateSuccess(t *testing.T) {
	srv := fakeGitHub(t, "v0.2.0", []byte("new"), sumsFor([]byte("new")), true)
	tu := newTestUpdater(t, srv.URL, "")
	if err := tu.update(t.Context(), "v0.1.6", "v0.2.0", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tu.bin); string(b) != "new" {
		t.Fatalf("binary is %q", b)
	}
	if b, _ := os.ReadFile(tu.bin + ".prev"); string(b) != "old" {
		t.Fatalf(".prev is %q", b)
	}
	if got := strings.Join(*tu.calls, ","); got != "restart" {
		t.Fatalf("systemctl calls %q", got)
	}
	info, err := readInfo(tu.infoPath)
	if err != nil || info.To != "v0.2.0" || info.From != "v0.1.6" {
		t.Fatalf("last-update.json: %+v, %v", info, err)
	}
	if _, err := os.Stat(info.Backup); err != nil {
		t.Fatalf("recorded backup missing: %v", err)
	}
}

func TestUpdateRevertsWhenUnhealthy(t *testing.T) {
	srv := fakeGitHub(t, "v0.2.0", []byte("new"), sumsFor([]byte("new")), true)
	tu := newTestUpdater(t, srv.URL, "")
	*tu.health = os.ErrDeadlineExceeded
	if err := tu.update(t.Context(), "v0.1.6", "v0.2.0", true); err == nil || !strings.Contains(err.Error(), "reverted") {
		t.Fatalf("unhealthy update: %v", err)
	}
	if b, _ := os.ReadFile(tu.bin); string(b) != "old" {
		t.Fatalf("binary not reverted: %q", b)
	}
	if got := strings.Join(*tu.calls, ","); got != "restart,stop,start" {
		t.Fatalf("systemctl calls %q", got)
	}
	conn, err := db.Open(tu.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Ping(); err != nil {
		t.Fatalf("database after revert: %v", err)
	}
}

func TestRollbackSwapsAndRestoresOnConfirm(t *testing.T) {
	tu := newTestUpdater(t, "", "y\n")
	if err := os.WriteFile(tu.bin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tu.bin+".prev", []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "gobill-pre-update.db")
	if err := os.WriteFile(backup, []byte("backup-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeInfo(tu.infoPath, updateInfo{From: "v0.1.6", To: "v0.2.0", Backup: backup}); err != nil {
		t.Fatal(err)
	}
	if err := tu.rollback(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tu.bin); string(b) != "old" {
		t.Fatalf("binary after rollback: %q", b)
	}
	if b, _ := os.ReadFile(tu.bin + ".prev"); string(b) != "new" {
		t.Fatalf(".prev after rollback should hold the newer binary: %q", b)
	}
	if b, _ := os.ReadFile(tu.dbPath); string(b) != "backup-bytes" {
		t.Fatal("database not restored after confirmation")
	}
	if got := strings.Join(*tu.calls, ","); got != "stop,start" {
		t.Fatalf("systemctl calls %q", got)
	}
}

func TestRollbackKeepsDatabaseWithoutConfirm(t *testing.T) {
	tu := newTestUpdater(t, "", "n\n")
	if err := os.WriteFile(tu.bin+".prev", []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(tu.dbPath)
	backup := filepath.Join(t.TempDir(), "b.db")
	os.WriteFile(backup, []byte("backup-bytes"), 0o600)
	if err := writeInfo(tu.infoPath, updateInfo{To: "v0.2.0", Backup: backup}); err != nil {
		t.Fatal(err)
	}
	if err := tu.rollback(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(tu.dbPath); !bytes.Equal(before, after) {
		t.Fatal("database changed without confirmation")
	}
	if !strings.Contains(tu.out.(*bytes.Buffer).String(), backup) {
		t.Fatal("rollback does not name the backup to restore")
	}
}

func TestRollbackNeedsPrevBinary(t *testing.T) {
	tu := newTestUpdater(t, "", "")
	if err := tu.rollback(); err == nil || !strings.Contains(err.Error(), "nothing to roll back") {
		t.Fatalf("rollback without .prev: %v", err)
	}
	if len(*tu.calls) != 0 {
		t.Fatalf("systemctl called: %v", *tu.calls)
	}
}

func TestReadConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.env")
	os.WriteFile(p, []byte("# c\nGOBILL_HTTP=:9090\nGOBILL_DB=\"/x/gobill.db\"\n"), 0o600)
	cfg, err := readConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfgOr(cfg, "GOBILL_HTTP", ":8080") != ":9090" || cfgOr(cfg, "GOBILL_DB", "") != "/x/gobill.db" || cfgOr(cfg, "GOBILL_BACKUP_DIR", "d") != "d" {
		t.Fatalf("config %v", cfg)
	}
}
