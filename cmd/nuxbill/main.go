// Command nuxbill runs the NuxBill admin server.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/job"
	"github.com/frand-kod/nuxbill-go/internal/notify"
	"github.com/frand-kod/nuxbill-go/internal/radius"
	"github.com/frand-kod/nuxbill-go/internal/secret"
	"github.com/frand-kod/nuxbill-go/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	run := run
	if len(os.Args) > 1 && os.Args[1] == "import" {
		run = func() error { return runImport(os.Args[2:]) }
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	dbPath := env("NUXBILL_DB", "./nuxbill.db")
	addr := env("NUXBILL_HTTP", ":8080")
	secure := os.Getenv("NUXBILL_HTTPS") == "1"

	conn, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		return err
	}
	if err := bootstrapAdmin(context.Background(), db.New(conn)); err != nil {
		return err
	}

	key, created, err := secret.LoadKey(os.Getenv("NUXBILL_SECRET_KEY"), dbPath+".key")
	if err != nil {
		return err
	}
	if created {
		slog.Warn("generated a new secret key; back it up with the database", "path", dbPath+".key")
	}

	app, err := web.New(conn, secure)
	if err != nil {
		return err
	}
	app.SecretKey = key
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

	svc := &billing.Service{DB: conn, Q: db.New(conn), Key: key}
	reload := func(ctx context.Context) {
		n, err := notify.Load(ctx, svc.Q)
		if err != nil {
			slog.Error("reload settings", "err", err)
			return
		}
		n.Log = notify.LogTo(svc.Q)
		loc := time.FixedZone("WIB", 7*3600)
		if l, err := time.LoadLocation("Asia/Jakarta"); err == nil {
			loc = l
		}
		if l, err := time.LoadLocation(n.Settings["timezone"]); err == nil {
			loc = l
		}
		svc.Reload(n, loc)
	}
	reload(ctx)
	app.SettingsChanged = func(ctx context.Context) { reload(ctx); app.ReloadSessionSettings(ctx) }
	app.Billing = svc
	go job.Run(ctx, "expiry", time.Minute, svc.ExpiryJob(guard.Trusted))
	go job.Run(ctx, "reminder", time.Minute, svc.ReminderJob(guard.Trusted))
	go job.Run(ctx, "router_check", 5*time.Minute, svc.RouterCheck)
	backup := &job.Backup{Conn: conn, Q: db.New(conn), Trusted: guard.Trusted,
		Dir: env("NUXBILL_BACKUP_DIR", filepath.Join(filepath.Dir(dbPath), "backup"))}
	go job.Run(ctx, "backup", time.Minute, backup.Run)

	errCh := make(chan error, 2)
	if ra := env("NUXBILL_RADIUS", ":1812"); ra != "" {
		host, port, err := net.SplitHostPort(ra)
		p, perr := strconv.Atoi(port)
		if err != nil || perr != nil {
			return fmt.Errorf("NUXBILL_RADIUS %q: want host:port", ra)
		}
		rs := &radius.Server{Q: db.New(conn), Key: key, Trusted: guard.Trusted,
			AuthAddr: ra, AcctAddr: net.JoinHostPort(host, strconv.Itoa(p+1))}
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

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	stop()
	<-guardDone
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// bootstrapAdmin creates the first SuperAdmin when the database has no admins.
// The random password is logged once; the operator must change it.
func bootstrapAdmin(ctx context.Context, q *db.Queries) error {
	n, err := q.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	password := randomPassword(16)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = q.CreateAdmin(ctx, db.CreateAdminParams{
		Username:     "admin",
		Fullname:     "Administrator",
		PasswordHash: string(hash),
		Role:         "SuperAdmin",
	})
	if err != nil {
		return err
	}
	slog.Warn("first admin created, change this password after signing in",
		"username", "admin", "password", password)
	return nil
}

func randomPassword(n int) string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	rand.Read(b) // never returns an error in Go 1.24+
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)] // slight modulo bias is fine for 54 chars
	}
	return string(b)
}
