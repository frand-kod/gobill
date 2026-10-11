package main

// `gobill update [--version vX.Y.Z] [--yes]` and `gobill rollback`: replace /usr/local/bin/gobill with a
// release from GitHub. Each update takes a database backup first (same folder and naming as the
// other pre-change backups) and keeps the old binary as <bin>.prev. A failed health check puts both
// back. Both commands need root and restart the gobill service.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/update"
)

const (
	binPath    = "/usr/local/bin/gobill" // ExecStart in deploy/gobill.service
	configPath = "/etc/gobill/config.env"
	healthWait = 60 * time.Second
)

// updateInfo is written to last-update.json next to the database before each update, so rollback
// can name the backup that matches the newer database.
type updateInfo struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Backup string `json:"backup"`
	At     string `json:"at"`
}

// updater holds the paths and the side effects (systemctl, health polling) so tests can stub them.
type updater struct {
	bin, dbPath, backupDir, infoPath, healthURL string
	apiURL                                      string
	client                                      *http.Client
	in                                          *bufio.Reader
	out                                         io.Writer
	systemctl                                   func(action string) error
	waitHealthy                                 func(url string, timeout time.Duration) error
}

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	want := fs.String("version", "", "install this release, e.g. v0.1.7 (default: the latest)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireRoot("update"); err != nil {
		return err
	}
	u, err := newUpdater(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	tag := *want
	if tag != "" && !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return u.update(context.Background(), version, tag, *yes)
}

func runRollback(args []string) error {
	if len(args) > 0 {
		return errors.New("usage: sudo gobill rollback")
	}
	if err := requireRoot("rollback"); err != nil {
		return err
	}
	u, err := newUpdater(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	return u.rollback()
}

func requireRoot(cmd string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("gobill %s must run as root: sudo gobill %s", cmd, cmd)
	}
	return nil
}

// newUpdater reads the paths and the HTTP port from the service's config file, as systemd does.
func newUpdater(in io.Reader, out io.Writer) (*updater, error) {
	cfg, err := readConfig(configPath)
	if err != nil {
		return nil, err
	}
	dbPath := cfgOr(cfg, "GOBILL_DB", "/var/lib/gobill/gobill.db")
	_, port, err := net.SplitHostPort(cfgOr(cfg, "GOBILL_HTTP", ":8080"))
	if err != nil {
		return nil, fmt.Errorf("GOBILL_HTTP in %s: want host:port", configPath)
	}
	return &updater{
		bin:         binPath,
		dbPath:      dbPath,
		backupDir:   cfgOr(cfg, "GOBILL_BACKUP_DIR", filepath.Join(filepath.Dir(dbPath), "backup")),
		infoPath:    filepath.Join(filepath.Dir(dbPath), "last-update.json"),
		healthURL:   "http://127.0.0.1:" + port + "/health",
		apiURL:      update.APIURL,
		client:      &http.Client{Timeout: 30 * time.Second},
		in:          bufio.NewReader(in),
		out:         out,
		systemctl:   systemctl,
		waitHealthy: waitHealthy,
	}, nil
}

func (u *updater) update(ctx context.Context, current, want string, yes bool) error {
	if want == "" {
		if _, ok := update.Parse(current); !ok {
			return fmt.Errorf("this is a development build (%s): use --version vX.Y.Z to install a release", current)
		}
	}
	url := u.apiURL + "/latest"
	if want != "" {
		url = u.apiURL + "/tags/" + want
	}
	rel, err := update.Fetch(ctx, u.client, url)
	if err != nil {
		return err
	}
	if rel.Tag == current || (want == "" && !update.Newer(rel.Tag, current)) {
		fmt.Fprintf(u.out, "already up to date (%s)\n", current)
		return nil
	}

	name, err := update.AssetName(runtime.GOARCH, buildGOARM())
	if err != nil {
		return err
	}
	binURL, sumsURL := assetURL(rel, name), assetURL(rel, "SHA256SUMS")
	if binURL == "" {
		return fmt.Errorf("release %s has no %s file", rel.Tag, name)
	}
	if sumsURL == "" {
		return fmt.Errorf("release %s has no SHA256SUMS; refusing to install an unverified binary", rel.Tag)
	}

	fmt.Fprintf(u.out, "current %s -> new %s\n", current, rel.Tag)
	if notes := firstLines(rel.Notes, 10); notes != "" {
		fmt.Fprintf(u.out, "\n%s\n\n", notes)
	}
	fmt.Fprintf(u.out, "release page: %s\n", rel.URL)
	if !yes && !u.confirm("Install "+rel.Tag+" now? [y/N] ") {
		fmt.Fprintln(u.out, "cancelled")
		return nil
	}

	// Both downloads go next to the binary, so the final rename stays on one filesystem.
	dir := filepath.Dir(u.bin)
	newBin, err := download(ctx, binURL, dir)
	if err != nil {
		return err
	}
	defer os.Remove(newBin)
	sumsPath, err := download(ctx, sumsURL, dir)
	if err != nil {
		return err
	}
	defer os.Remove(sumsPath)
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	if err := update.Verify(newBin, sums, name); err != nil {
		return err
	}
	if err := os.Chmod(newBin, 0o755); err != nil {
		return err
	}

	backup, err := u.backupDB(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(u.out, "database backup: %s\n", backup)
	info := updateInfo{From: current, To: rel.Tag, Backup: backup, At: time.Now().Format(time.RFC3339)}
	if err := writeInfo(u.infoPath, info); err != nil {
		return err
	}

	prev := u.bin + ".prev"
	if err := copyFile(u.bin, prev, 0o755); err != nil {
		return fmt.Errorf("keep the previous binary: %w", err)
	}
	if err := os.Rename(newBin, u.bin); err != nil {
		return err
	}
	fmt.Fprintf(u.out, "installed %s, restarting gobill\n", rel.Tag)

	if err := u.systemctl("restart"); err != nil {
		return u.failed(current, prev, backup, err)
	}
	if err := u.waitHealthy(u.healthURL, healthWait); err != nil {
		return u.failed(current, prev, backup, err)
	}
	fmt.Fprintf(u.out, "updated to %s and healthy\n", rel.Tag)
	fmt.Fprintln(u.out, "to go back: sudo gobill rollback")
	return nil
}

// failed reverts after a restart or health check failed, then reports the cause.
func (u *updater) failed(current, prev, backup string, cause error) error {
	fmt.Fprintf(u.out, "update failed: %v\nrestoring the previous binary and the database backup\n", cause)
	if err := u.revert(prev, backup); err != nil {
		return fmt.Errorf("update failed (%v) and the revert did not finish: %w; restore by hand, see docs/en/upgrade.md", cause, err)
	}
	return fmt.Errorf("update failed, reverted to %s: %w", current, cause)
}

// revert stops the service, restores the database backup and the previous binary, and starts it again.
func (u *updater) revert(prev, backup string) error {
	var errs []error
	keep := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	keep(u.systemctl("stop"))
	keep(restoreDB(backup, u.dbPath))
	keep(copyFile(prev, u.bin, 0o755))
	keep(u.systemctl("start"))
	return errors.Join(errs...)
}

func (u *updater) rollback() error {
	prev := u.bin + ".prev"
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("no previous binary at %s: nothing to roll back to", prev)
	}
	info, _ := readInfo(u.infoPath) // missing or unreadable: no backup to suggest
	if err := u.systemctl("stop"); err != nil {
		return err
	}
	if err := swapBinary(u.bin, prev); err != nil {
		return err
	}
	fmt.Fprintf(u.out, "binary swapped; %s now holds the other version, so running rollback again undoes this\n", prev)
	if info != nil && info.Backup != "" {
		fmt.Fprintf(u.out, "\nThe update to %s may have run database migrations.\n", info.To)
		fmt.Fprintf(u.out, "Backup taken before that update: %s\n", info.Backup)
		if u.confirm("Restore this backup now? It replaces the current database. [y/N] ") {
			if err := restoreDB(info.Backup, u.dbPath); err != nil {
				return err
			}
			fmt.Fprintln(u.out, "database restored")
		} else {
			fmt.Fprintf(u.out, "database not restored. To restore later, with gobill stopped:\n  sudo cp %s %s\n  sudo rm -f %s-wal %s-shm\n",
				info.Backup, u.dbPath, u.dbPath, u.dbPath)
		}
	}
	if err := u.systemctl("start"); err != nil {
		return err
	}
	if err := u.waitHealthy(u.healthURL, healthWait); err != nil {
		return fmt.Errorf("gobill started but is not healthy: %w", err)
	}
	fmt.Fprintln(u.out, "rolled back and healthy")
	return nil
}

// backupDB takes a VACUUM INTO copy of the live database into the backup folder. The new file and
// folder get the database owner, so the service user can still prune and read them.
func (u *updater) backupDB(ctx context.Context) (string, error) {
	conn, err := db.Open(u.dbPath)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	name, err := db.Snapshot(ctx, conn, u.backupDir, "pre-update")
	if err != nil {
		return "", fmt.Errorf("database backup: %w", err)
	}
	path := filepath.Join(u.backupDir, name)
	if err := chownLike(u.dbPath, u.backupDir, path); err != nil {
		return "", err
	}
	return path, nil
}

func (u *updater) confirm(prompt string) bool {
	fmt.Fprint(u.out, prompt)
	line, _ := u.in.ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes"
}

// download saves url to a new file in dir and returns its path. The file is 0600 until the caller
// chmods it.
func download(ctx context.Context, url, dir string) (string, error) {
	f, err := os.CreateTemp(dir, ".gobill-update-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	err = func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("download %s: %s", url, resp.Status)
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			return err
		}
		return f.Close()
	}()
	if err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// copyFile copies src to dst through a temp file in dst's folder, so dst is replaced atomically.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".gobill-copy-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, in)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// swapBinary exchanges bin and prev, so a second rollback goes forward again.
func swapBinary(bin, prev string) error {
	tmp := bin + ".swap"
	if err := os.Rename(bin, tmp); err != nil {
		return err
	}
	if err := os.Rename(prev, bin); err != nil {
		os.Rename(tmp, bin)
		return err
	}
	if err := os.Rename(tmp, prev); err != nil {
		os.Rename(bin, prev)
		os.Rename(tmp, bin)
		return err
	}
	return nil
}

// restoreDB writes backup over the database file in place, so its owner and mode stay as the
// service expects, then removes the -wal and -shm files that belong to the old data. Only call it
// while gobill is stopped.
func restoreDB(backup, dbPath string) error {
	in, err := os.Open(backup)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dbPath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
	return nil
}

// chownLike gives each path the owner and group of ref.
func chownLike(ref string, paths ...string) error {
	st, err := os.Stat(ref)
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	for _, p := range paths {
		if err := os.Chown(p, int(sys.Uid), int(sys.Gid)); err != nil {
			return err
		}
	}
	return nil
}

func writeInfo(path string, info updateInfo) error {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// readInfo returns nil and an error when the file is missing.
func readInfo(path string) (*updateInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info updateInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// systemctl runs one action on the gobill unit.
func systemctl(action string) error {
	out, err := exec.Command("systemctl", action, "gobill").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s gobill: %v: %s", action, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// waitHealthy polls url until it answers 200 or the timeout passes.
func waitHealthy(url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	for {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("status %s", resp.Status)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s not healthy after %s: %w", url, timeout, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// buildGOARM is the GOARM the binary was compiled with, or "" off arm.
func buildGOARM() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "GOARM" {
			return s.Value
		}
	}
	return ""
}

// assetURL returns the download URL of the named release file, or "".
func assetURL(rel update.Release, name string) string {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, "\n")
}

// readConfig reads KEY=VALUE lines in the format systemd's EnvironmentFile uses.
func readConfig(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return m, nil
}

func cfgOr(cfg map[string]string, key, def string) string {
	if v := cfg[key]; v != "" {
		return v
	}
	return def
}
