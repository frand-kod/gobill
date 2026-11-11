// Command gobill runs the gobill admin server.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/app"
	"github.com/frand-kod/gobill/internal/config"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// exitRestart tells systemd (Restart=on-failure) to start gobill again after a staged restore.
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
	cfg, cfgErr := config.Load() // a bad duration only stops the server, as before
	run := func() error {
		if cfgErr != nil {
			return cfgErr
		}
		return app.Run(cfg, version)
	}
	if len(os.Args) > 1 {
		args := os.Args[2:]
		switch os.Args[1] {
		case "import":
			run = func() error { return runImport(cfg, args) }
		case "reset-admin-password":
			run = func() error { return runResetAdminPassword(cfg, args) }
		case "update":
			run = func() error { return runUpdate(args) }
		case "verify-release":
			run = func() error { return runVerifyRelease(args) }
		case "healthcheck":
			run = func() error { return runHealthcheck(cfg, args) }
		case "rollback":
			run = func() error { return runRollback(args) }
		}
	}
	if err := run(); errors.Is(err, app.ErrRestart) {
		slog.Warn("exiting for restart after restore", "code", exitRestart)
		os.Exit(exitRestart)
	} else if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}
