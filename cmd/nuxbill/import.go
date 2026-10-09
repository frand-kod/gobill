package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/importer"
	"github.com/frand-kod/gobill/internal/secret"
)

// runImport implements `nuxbill import`: PHPNuxBill MySQL -> SQLite.
func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	dsn := fs.String("mysql-dsn", "", "MySQL DSN, e.g. user:pass@tcp(127.0.0.1:3306)/phpnuxbill")
	dbPath := fs.String("db", env("NUXBILL_DB", "./nuxbill.db"), "target SQLite file")
	tz := fs.String("timezone", "", "timezone of the old dates (default: old appconfig timezone)")
	dry := fs.Bool("dry-run", false, "read and convert everything, then roll back")
	force := fs.Bool("force", false, "wipe a non-empty target first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn == "" {
		return fmt.Errorf("--mysql-dsn is required")
	}
	my, err := sql.Open("mysql", *dsn)
	if err != nil {
		return err
	}
	defer my.Close()
	ctx := context.Background()
	if err := my.PingContext(ctx); err != nil {
		return err
	}
	if *tz == "" {
		*tz = importer.Timezone(ctx, my)
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return err
	}
	key, _, err := secret.LoadKey(os.Getenv("NUXBILL_SECRET_KEY"), *dbPath+".key")
	if err != nil {
		return err
	}
	lite, err := db.Open(*dbPath)
	if err != nil {
		return err
	}
	defer lite.Close()
	if err := db.Migrate(lite); err != nil {
		return err
	}
	rep, err := importer.Run(ctx, my, lite, importer.Options{Key: key, Loc: loc, DryRun: *dry, Force: *force})
	if err != nil {
		return err
	}
	rep.Print(os.Stdout)
	if *dry {
		fmt.Println("dry run: nothing was written")
	}
	return nil
}
