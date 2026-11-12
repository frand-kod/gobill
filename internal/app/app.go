// Package app wires the gobill server: open the database, build the services, start the
// background jobs and serve HTTP and RADIUS.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/service/restore"
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

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/config"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/job"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/selfupdate"
	"github.com/frand-kod/gobill/internal/web"
	"github.com/frand-kod/gobill/internal/web/admin/network"
	"github.com/frand-kod/gobill/internal/wg"
)

// ErrRestart ends Run after a database restore is staged. main exits with a code that systemd
// (Restart=on-failure) treats as a failure, so it starts the app again.
var ErrRestart = errors.New("restart after restore")

// Open applies a staged restore, opens and migrates the database, creates the first admin and
// loads the secret key. The caller closes the connection.
func Open(cfg config.Config) (*sql.DB, []byte, error) {
	dbPath := cfg.DB
	if err := db.ApplyPendingRestore(dbPath); err != nil {
		return nil, nil, fmt.Errorf("restore: %w", err)
	}
	minProcs()
	if budget, src := tuneMemory(); budget > 0 {
		slog.Info("memory budget", "mib", budget>>20, "from", src, "sqlite_cache_kib_per_conn", db.CacheKiB)
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	key, err := prepare(cfg, conn)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, key, nil
}

func prepare(cfg config.Config, conn *sql.DB) ([]byte, error) {
	dbPath := cfg.DB
	if err := db.Migrate(conn); err != nil {
		return nil, err
	}
	changed, err := db.NormalisePhones(context.Background(), conn)
	if err != nil {
		return nil, fmt.Errorf("normalise phones: %w", err)
	}
	slog.Info("customer phones normalised", "changed", changed)
	if err := bootstrapAdmin(context.Background(), db.New(conn), filepath.Dir(dbPath)); err != nil {
		return nil, err
	}

	key, created, err := secret.LoadKey(cfg.SecretKey, dbPath+".key")
	if err != nil {
		return nil, err
	}
	if created {
		if !cfg.AllowNewKey {
			if what := sealedRows(conn); what != "" {
				os.Remove(dbPath + ".key") // do not keep a key that cannot open the existing data
				return nil, fmt.Errorf("the key file %s.key was missing and the database already holds sealed values (%s); a new key cannot open them. Restore the original key file (or set GOBILL_SECRET_KEY), or set GOBILL_ALLOW_NEW_KEY=1 to start with a new key and accept losing those secrets", dbPath, what)
			}
		}
		slog.Warn("generated a new secret key; back it up with the database", "path", dbPath+".key")
	}

	db.SettingsKey = key
	if err := db.EncryptSettings(context.Background(), conn); err != nil {
		return nil, fmt.Errorf("encrypt settings: %w", err)
	}
	return key, nil
}

// Job is one background job, run every Every by job.Run.
type Job struct {
	Name  string
	Every time.Duration
	Fn    func(context.Context) error
}

// parts are the objects the background jobs run on.
type parts struct {
	conn    *sql.DB
	key     []byte
	dbPath  string
	svc     *billing.Service
	web     *web.Server
	guard   *job.ClockGuard
	backup  *job.Backup
	updates *selfupdate.Check
	wg      *wg.Manager
}

// jobs lists every background job. Run starts them unless GOBILL_JOBS=0.
func jobs(p parts) []Job {
	svc, trusted := p.svc, p.guard.Trusted
	alerts := &billing.AlertJob{S: svc, DBPath: p.dbPath, Free: func() (int64, error) { return job.DiskFreeMB(filepath.Dir(p.dbPath)) }}
	return []Job{
		{"expiry", time.Minute, svc.ExpiryJob(trusted)},
		{"reminder", time.Minute, svc.ReminderJob(trusted)},
		{"upload_sweep", 10 * time.Minute, p.web.SweepUploads},
		{"log_clean", 10 * time.Minute, svc.LogCleanJob(trusted)}, // once a day, setting log_keep_days
		{"router_check", 5 * time.Minute, svc.RouterCheck},
		{"router_pending", 5 * time.Minute, svc.RetryPending},
		{"alert", time.Minute, alerts.Run},
		{"daily_summary", time.Minute, svc.DailySummaryJob(trusted)},
		{"backup", time.Minute, p.backup.Run},
		{"update_check", time.Minute, p.updates.Run},
		{"fup", time.Minute, radius.FUPJob(db.New(p.conn), p.key, "")},
		{"boost", time.Minute, radius.BoostJob(db.New(p.conn), p.key, "")},
		{"wg_endpoints", time.Minute, p.wg.SaveEndpoints},
	}
}

// Run serves gobill until a signal, a listener error or a restart request.
func Run(cfg config.Config, version string) error {
	dbPath, addr, secure := cfg.DB, cfg.HTTP, cfg.Secure
	if a := cfg.PProf; a != "" { // profiling for bench/; unauthenticated, so bind it to loopback
		slog.Warn("GOBILL_PPROF: profiler listening, do not expose it", "addr", a)
		go func() { slog.Error("pprof", "err", http.ListenAndServe(a, nil)) }()
	}
	if !secure {
		slog.Warn("GOBILL_HTTPS=0: session cookies are sent without the Secure flag")
	}
	slog.Info("gobill starting", "version", version)

	tuneWindows(cfg)

	backupDir := cfg.Backup(dbPath)
	conn, key, err := Open(cfg)
	if err != nil {
		return err
	}
	defer conn.Close()

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
	if err := db.BackfillUsageDaily(ctx, conn, svc.Location()); err != nil {
		return fmt.Errorf("backfill daily usage: %w", err)
	}
	marker := filepath.Join(filepath.Dir(dbPath), ".running") // stays only after a crash or kill
	if err := svc.StartMarker(marker); err != nil {
		slog.Error("running marker", "err", err)
	}
	app.SettingsChanged = func(ctx context.Context) { reload(ctx); app.ReloadSessionSettings(ctx) }
	app.Billing = svc
	backup := &job.Backup{Conn: conn, Q: db.New(conn), Trusted: guard.Trusted, Dir: backupDir, KeyFP: restore.KeyFingerprint(key),
		Mirror: cfg.BackupMirror}
	app.BackupDir, app.BackupMirror = backup.Dir, backup.Mirror
	updates := &selfupdate.Check{Q: db.New(conn)}
	app.UpdateInfo = updates.Latest
	restartCh := make(chan struct{}, 1)
	app.Restart = func() {
		select {
		case restartCh <- struct{}{}:
		default:
		}
	}

	errCh := make(chan error, 2)
	rs := &radius.Server{Settings: app.Settings, Q: db.NewCached(conn), DB: conn, Key: key, Loc: svc.Location, Trusted: guard.Trusted, Redeem: svc.RedeemVoucher, Start: svc.StartPending}
	radius.AppLoc = svc.Location
	app.Radius = rs // shared with /radius.php so throttles and dedup state are common
	if ra := cfg.RadiusAddr; cfg.RadiusOn {
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
	// WireGuard: off unless the setting says yes, and GOBILL_WG=off beats the setting. RADIUS is served inside
	// the tunnel on the same ports as the LAN socket.
	wgm := &wg.Manager{Q: db.New(conn), Key: key, AuthPort: 1812, AcctPort: 1813}
	if rs.AuthAddr != "" {
		wgm.Radius = rs
		_, p, _ := net.SplitHostPort(rs.AuthAddr)
		wgm.AuthPort, _ = strconv.Atoi(p)
		wgm.AcctPort = wgm.AuthPort + 1
	}
	app.WG = wgm
	wgm.Reload(ctx)
	if n, _ := db.New(conn).CountWgPeers(ctx); n > 0 && wg.Disabled() {
		slog.Warn("GOBILL_WG=off: routers with a WireGuard tunnel are cut off from gobill", "routers", n)
	}
	if cfg.Jobs {
		for _, j := range jobs(parts{conn: conn, key: key, dbPath: dbPath, svc: svc, web: app, guard: guard,
			backup: backup, updates: updates, wg: wgm}) {
			go job.Run(ctx, j.Name, j.Every, j.Fn)
		}
	} else {
		slog.Warn("GOBILL_JOBS=0: background jobs are off in this process")
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
	shutErr := srv.Shutdown(shutCtx)
	os.Remove(marker) // stopped on purpose, even if Shutdown timed out: the next start must not report a crash
	if shutErr != nil && !errors.Is(shutErr, http.ErrServerClosed) {
		return shutErr
	}
	if restarting {
		return ErrRestart
	}
	return nil
}

// tuneWindows applies the GOBILL_STALE_AFTER and GOBILL_NAS_ONLINE_WINDOW overrides.
// config.Load already rejected bad values.
func tuneWindows(cfg config.Config) {
	if d := cfg.StaleAfter; d > 0 {
		radius.StaleAfter = int64(d / time.Second)
		slog.Info("RADIUS stale window overridden", "GOBILL_STALE_AFTER", d)
	}
	if d := cfg.NASOnlineWindow; d > 0 {
		network.NASFreshFor = d
		slog.Info("NAS online window overridden", "GOBILL_NAS_ONLINE_WINDOW", d)
	}
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

// sealedRows names the tables that hold encrypted values, or "" when there are none.
func sealedRows(conn *sql.DB) string {
	var found []string
	for _, c := range []struct{ name, query string }{
		{"customers.secret_enc", "SELECT EXISTS(SELECT 1 FROM customers WHERE length(secret_enc) > 0)"},
		{"nas.secret_enc", "SELECT EXISTS(SELECT 1 FROM nas WHERE length(secret_enc) > 0)"},
		{"routers.password_enc", "SELECT EXISTS(SELECT 1 FROM routers WHERE length(password_enc) > 0)"},
		{"settings", "SELECT EXISTS(SELECT 1 FROM settings WHERE value LIKE 'enc1:%')"},
	} {
		var n int
		if conn.QueryRow(c.query).Scan(&n) == nil && n > 0 {
			found = append(found, c.name)
		}
	}
	return strings.Join(found, ", ")
}
