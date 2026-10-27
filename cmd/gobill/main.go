// Command gobill runs the gobill admin server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // DefaultServeMux only; served by GOBILL_PPROF, never by the app mux
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/job"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// errRestart ends run after a database restore is staged. main exits with exitRestart, which
// systemd (Restart=on-failure) treats as a failure and starts the app again.
var errRestart = errors.New("restart after restore")

const exitRestart = 3

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "config-example" {
		fmt.Print(gobill.ConfigExample)
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	run := run
	if len(os.Args) > 1 && os.Args[1] == "import" {
		run = func() error { return runImport(os.Args[2:]) }
	}
	if len(os.Args) > 1 && os.Args[1] == "reset-admin-password" {
		run = func() error { return runResetAdminPassword(os.Args[2:]) }
	}
	if len(os.Args) > 1 && os.Args[1] == "update" {
		run = func() error { return runUpdate(os.Args[2:]) }
	}
	if len(os.Args) > 1 && os.Args[1] == "verify-release" {
		run = func() error { return runVerifyRelease(os.Args[2:]) }
	}
	if len(os.Args) > 1 && os.Args[1] == "rollback" {
		run = func() error { return runRollback(os.Args[2:]) }
	}
	if err := run(); errors.Is(err, errRestart) {
		slog.Warn("exiting for restart after restore", "code", exitRestart)
		os.Exit(exitRestart)
	} else if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	dbPath := env("GOBILL_DB", "./gobill.db")
	addr := env("GOBILL_HTTP", ":8080")
	secure := os.Getenv("GOBILL_HTTPS") != "0"
	if a := os.Getenv("GOBILL_PPROF"); a != "" { // profiling for bench/; unauthenticated, so bind it to loopback
		slog.Warn("GOBILL_PPROF: profiler listening, do not expose it", "addr", a)
		go func() { slog.Error("pprof", "err", http.ListenAndServe(a, nil)) }()
	}
	if !secure {
		slog.Warn("GOBILL_HTTPS=0: session cookies are sent without the Secure flag")
	}
	slog.Info("gobill starting", "version", version)

	if err := tuneWindows(); err != nil {
		return err
	}

	backupDir := env("GOBILL_BACKUP_DIR", filepath.Join(filepath.Dir(dbPath), "backup"))
	if err := db.ApplyPendingRestore(dbPath); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	minProcs()
	if budget, src := tuneMemory(); budget > 0 {
		slog.Info("memory budget", "mib", budget>>20, "from", src, "sqlite_cache_kib_per_conn", db.CacheKiB)
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		return err
	}
	changed, err := db.NormalisePhones(context.Background(), conn)
	if err != nil {
		return fmt.Errorf("normalise phones: %w", err)
	}
	slog.Info("customer phones normalised", "changed", changed)
	if err := bootstrapAdmin(context.Background(), db.New(conn), filepath.Dir(dbPath)); err != nil {
		return err
	}

	key, created, err := secret.LoadKey(os.Getenv("GOBILL_SECRET_KEY"), dbPath+".key")
	if err != nil {
		return err
	}
	if created {
		slog.Warn("generated a new secret key; back it up with the database", "path", dbPath+".key")
	}

	db.SettingsKey = key
	if err := db.EncryptSettings(context.Background(), conn); err != nil {
		return fmt.Errorf("encrypt settings: %w", err)
	}
	app, err := web.New(conn, secure)
	if err != nil {
		return err
	}
	app.SecretKey = key
	app.Version = version
	app.DBPath = dbPath
	app.ListenAddr = addr
	guard := job.NewClockGuard(db.New(conn))
	app.ClockWarning = guard.Reason
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	guardDone := make(chan struct{})
	go func() {
		defer close(guardDone)
		guard.Run(ctx)
	}()

	svc := &billing.Service{DB: conn, Q: db.NewCached(conn), Key: key}
	reload := func(ctx context.Context) {
		n, err := notify.Load(ctx, svc.Q)
		if err != nil {
			slog.Error("reload settings", "err", err)
			return
		}
		loc := loadZone("Asia/Jakarta", time.FixedZone("WIB", 7*3600))
		svc.Reload(n, loadZone(n.Settings["timezone"], loc))
	}
	reload(ctx)
	marker := filepath.Join(filepath.Dir(dbPath), ".running") // stays only after a crash or kill
	if err := svc.StartMarker(marker); err != nil {
		slog.Error("running marker", "err", err)
	}
	app.SettingsChanged = func(ctx context.Context) { reload(ctx); app.ReloadSessionSettings(ctx) }
	app.Billing = svc
	go job.Run(ctx, "expiry", time.Minute, svc.ExpiryJob(guard.Trusted))
	go job.Run(ctx, "reminder", time.Minute, svc.ReminderJob(guard.Trusted))
	go job.Run(ctx, "log_clean", 10*time.Minute, svc.LogCleanJob(guard.Trusted)) // once a day, setting log_keep_days
	go job.Run(ctx, "router_check", 5*time.Minute, svc.RouterCheck)
	go job.Run(ctx, "online_sample", 5*time.Minute, app.SampleOnline)
	alerts := &billing.AlertJob{S: svc, DBPath: dbPath, Free: func() (int64, error) { return job.DiskFreeMB(filepath.Dir(dbPath)) }}
	go job.Run(ctx, "alert", time.Minute, alerts.Run)
	go job.Run(ctx, "daily_summary", time.Minute, svc.DailySummaryJob(guard.Trusted))
	backup := &job.Backup{Conn: conn, Q: db.New(conn), Trusted: guard.Trusted, Dir: backupDir,
		Mirror: env("GOBILL_BACKUP_MIRROR", "")}
	go job.Run(ctx, "backup", time.Minute, backup.Run)
	app.BackupDir, app.BackupMirror = backup.Dir, backup.Mirror
	updates := &job.UpdateCheck{Q: db.New(conn)}
	go job.Run(ctx, "update_check", time.Minute, updates.Run)
	app.UpdateInfo = updates.Latest
	restartCh := make(chan struct{}, 1)
	app.Restart = func() {
		select {
		case restartCh <- struct{}{}:
		default:
		}
	}

	errCh := make(chan error, 2)
	rs := &radius.Server{Q: db.NewCached(conn), DB: conn, Key: key, Trusted: guard.Trusted, Redeem: svc.RedeemVoucher, Start: svc.StartPending}
	go job.Run(ctx, "fup", time.Minute, radius.FUPJob(db.New(conn), key, ""))
	app.Radius = rs // shared with /radius.php so throttles and dedup state are common
	if ra, on := radiusAddr(); on {
		host, port, err := net.SplitHostPort(ra)
		p, perr := strconv.Atoi(port)
		if err != nil || perr != nil {
			return fmt.Errorf("GOBILL_RADIUS %q: want host:port", ra)
		}
		rs.AuthAddr, rs.AcctAddr = ra, net.JoinHostPort(host, strconv.Itoa(p+1))
		go func() {
			slog.Info("radius listening", "auth", rs.AuthAddr, "acct", rs.AcctAddr)
			if err := rs.ListenAndServe(ctx); err != nil {
				errCh <- err
			}
		}()
	}
	go func() {
		slog.Info("listening", "addr", addr, "db", dbPath)
		errCh <- srv.ListenAndServe()
	}()

	restarting := false
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	case <-restartCh:
		restarting = true
	}
	slog.Info("shutting down")
	stop()
	<-guardDone
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	os.Remove(marker) // clean stop: the next start must not report a crash
	if restarting {
		return errRestart
	}
	return nil
}

// radiusAddr returns the UDP RADIUS listen address. Unset means ":1812";
// empty or "off" disables the UDP listener (the REST /radius.php still works).
func radiusAddr() (addr string, enabled bool) {
	v, ok := os.LookupEnv("GOBILL_RADIUS")
	if !ok {
		return ":1812", true
	}
	if v == "" || v == "off" {
		return "", false
	}
	return v, true
}

// tuneWindows applies the GOBILL_STALE_AFTER and GOBILL_NAS_ONLINE_WINDOW overrides. A bad value
// stops startup, so a typo never silently keeps the default. See config.env.example.
func tuneWindows() error {
	if d, ok, err := envDuration("GOBILL_STALE_AFTER", time.Minute, time.Hour); err != nil {
		return err
	} else if ok {
		radius.StaleAfter = int64(d / time.Second)
		slog.Info("RADIUS stale window overridden", "GOBILL_STALE_AFTER", d)
	}
	if d, ok, err := envDuration("GOBILL_NAS_ONLINE_WINDOW", time.Minute, 24*time.Hour); err != nil {
		return err
	} else if ok {
		web.NASFreshFor = d
		slog.Info("NAS online window overridden", "GOBILL_NAS_ONLINE_WINDOW", d)
	}
	return nil
}

// envDuration reads a Go duration ("10m", "90s") from key; ok is false when the key is unset.
func envDuration(key string, lo, hi time.Duration) (d time.Duration, ok bool, err error) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false, nil
	}
	if d, err = time.ParseDuration(v); err != nil || d < lo || d > hi {
		return 0, false, fmt.Errorf("%s=%q: want a duration between %s and %s, e.g. 10m", key, v, lo, hi)
	}
	return d, true, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// bootstrapAdmin creates the first SuperAdmin when the database has no admins.
// The random password is written to initial-admin-password.txt in dir (0600, never logged);
// the operator must read it, sign in and change it, then delete the file.
func bootstrapAdmin(ctx context.Context, q *db.Queries, dir string) error {
	n, err := q.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	password := secret.RandomPassword(16)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "initial-admin-password.txt")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("initial admin password file %s: %w (remove it to bootstrap again)", path, err)
	}
	_, err = fmt.Fprintln(f, password)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		_, err = q.CreateAdmin(ctx, db.CreateAdminParams{
			Username:     "admin",
			Fullname:     "Administrator",
			PasswordHash: string(hash),
			Role:         "SuperAdmin",
		})
	}
	if err != nil {
		os.Remove(path)
		return err
	}
	slog.Warn("first admin created: read the password from the file, sign in, change it, then delete the file",
		"username", "admin", "file", path)
	return nil
}

// loadZone returns the named zone, or def when name is blank or unknown.
// time.LoadLocation("") would return UTC, so blank must not reach it.
func loadZone(name string, def *time.Location) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		return def
	}
	if l, err := time.LoadLocation(name); err == nil {
		return l
	}
	return def
}
