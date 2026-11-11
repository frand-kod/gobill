package main

// `gobill update`, `gobill rollback` and `gobill healthcheck`: argument parsing only. The work is
// in internal/selfupdate.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/frand-kod/gobill/internal/config"
	"github.com/frand-kod/gobill/internal/selfupdate"
)

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	want := fs.String("version", "", "install this release, e.g. v0.1.7 (default: the latest)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := selfupdate.RequireRoot("update"); err != nil {
		return err
	}
	u, err := selfupdate.New(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	tag := *want
	if tag != "" && !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return u.Update(context.Background(), version, tag, *yes)
}

func runRollback(args []string) error {
	if len(args) > 0 {
		return errors.New("usage: sudo gobill rollback")
	}
	if err := selfupdate.RequireRoot("rollback"); err != nil {
		return err
	}
	u, err := selfupdate.New(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	return u.Rollback()
}

// runHealthcheck is `gobill healthcheck`: GET /health on the local port (GOBILL_HTTP, default :8080)
// and exit 0 if it answers 200, 1 otherwise. The Docker HEALTHCHECK uses it, because the distroless
// image has no shell or curl.
func runHealthcheck(cfg config.Config, args []string) error {
	if len(args) > 0 {
		return errors.New("usage: gobill healthcheck")
	}
	_, port, err := net.SplitHostPort(cfg.HTTP)
	if err != nil {
		return fmt.Errorf("GOBILL_HTTP: want host:port: %w", err)
	}
	return selfupdate.CheckHealth("http://127.0.0.1:" + port + "/health")
}
