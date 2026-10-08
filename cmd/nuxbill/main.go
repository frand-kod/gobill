// Command nuxbill runs the NuxBill admin server.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/job"
	"github.com/frand-kod/nuxbill-go/internal/secret"
	"github.com/frand-kod/nuxbill-go/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
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

	loc := time.FixedZone("WIB", 7*3600)
	if l, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		loc = l
	}
	if rows, err := db.New(conn).ListSettings(ctx); err == nil {
		for _, r := range rows {
			if l, err := time.LoadLocation(r.Value); r.Key == "timezone" && err == nil {
				loc = l
			}
		}
	}
	svc := &billing.Service{DB: conn, Q: db.New(conn), Key: key, Loc: loc}
	app.Billing = svc
	go job.Run(ctx, "expiry", time.Minute, svc.ExpiryJob(guard.Trusted))

	errCh := make(chan error, 1)
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
