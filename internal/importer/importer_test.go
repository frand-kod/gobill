package importer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"

	"github.com/frand-kod/gobill/internal/db"
)

func TestParsePrice(t *testing.T) {
	ok := map[string]int64{"10000": 10000, "Rp. 10.000": 10000, "10,000": 10000, "1.500.000": 1500000, "10000.00": 10000,
		"1000.50": 1001, "10.000,50": 10001, "10,000.49": 10000, " 0 ": 0, "5.5": 6}
	for in, want := range ok {
		if got, err := ParsePrice(in); err != nil || got != want {
			t.Errorf("ParsePrice(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "-5000", "..."} {
		if _, err := ParsePrice(in); err == nil {
			t.Errorf("ParsePrice(%q) should fail", in)
		}
	}
}

func TestParseWhen(t *testing.T) {
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	want := time.Date(2024, 3, 5, 7, 30, 0, 0, time.UTC).Unix()
	if got, err := ParseWhen("2024-03-05", "14:30:00", jkt); err != nil || got != want {
		t.Fatalf("got %d, %v; want %d", got, err, want)
	}
	if got, _ := ParseWhen("2024-03-05 14:30:00", "", jkt); got != want {
		t.Fatalf("datetime form: %d", got)
	}
	for _, d := range []string{"0000-00-00", "", "junk"} {
		if _, err := ParseWhen(d, "00:00:00", jkt); err == nil {
			t.Errorf("ParseWhen(%q) should fail", d)
		}
	}
}

func TestStatusMapping(t *testing.T) {
	if SubStatus("on") != "active" || SubStatus("off") != "expired" || SubStatus("") != "expired" {
		t.Fatal("SubStatus")
	}
	if v, _ := PlanType("PPPOE"); v != "PPPoE" {
		t.Fatal("PlanType PPPOE")
	}
	if _, err := PlanType("VPN"); err == nil {
		t.Fatal("VPN should be unsupported")
	}
}

// TestImportFromMySQL needs a MySQL/MariaDB user that may create databases:
//
//	NUXBILL_TEST_MYSQL_DSN=user:pass@tcp(127.0.0.1:3306)/ NUXBILL_TEST_PHP_SQL=.../phpnuxbill/install/phpnuxbill.sql
func TestImportFromMySQL(t *testing.T) {
	dsn, schema := os.Getenv("NUXBILL_TEST_MYSQL_DSN"), os.Getenv("NUXBILL_TEST_PHP_SQL")
	if dsn == "" || schema == "" {
		t.Skip("NUXBILL_TEST_MYSQL_DSN / NUXBILL_TEST_PHP_SQL not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("mysql", dsn+"?multiStatements=true")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("nuxbill_import_test_%d", os.Getpid())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP DATABASE " + name)
	my, err := sql.Open("mysql", dsn+name+"?multiStatements=true")
	if err != nil {
		t.Fatal(err)
	}
	defer my.Close()
	ddl, err := os.ReadFile(schema)
	if err != nil {
		t.Fatal(err)
	}
	seed := `DELETE FROM tbl_users; DELETE FROM tbl_appconfig;
INSERT INTO tbl_appconfig (setting, value) VALUES ('timezone', 'Asia/Jakarta'), ('CompanyName', 'ACME');
INSERT INTO tbl_users (id, username, fullname, password, user_type, status, creationdate) VALUES
 (1, 'root', 'Root', SHA1('adminpw'), 'SuperAdmin', 'Active', '2023-01-01 00:00:00'), (2, 'bad', 'Bad', 'plain', 'Admin', 'Active', '2023-01-01 00:00:00');
INSERT INTO tbl_routers (id, name, ip_address, username, password, description) VALUES (1, 'r1', '10.0.0.1:8729', 'api', 'rpw', '');
INSERT INTO tbl_bandwidth (id, name_bw, rate_down, rate_down_unit, rate_up, rate_up_unit) VALUES (1, '5M', 5, 'Mbps', 5, 'Mbps');
INSERT INTO tbl_pool (id, pool_name, local_ip, range_ip, routers) VALUES (1, 'pool1', '10.1.0.1', '10.1.0.2-10.1.0.99', 'r1');
INSERT INTO tbl_plans (id, name_plan, id_bw, price, type, typebp, validity, validity_unit, routers, pool, enabled, prepaid, device) VALUES
 (1, 'Gold', 1, 'Rp. 10.000', 'PPPOE', 'Unlimited', 1, 'Months', 'r1', 'pool1', 1, 'yes', 'MikrotikPppoe'),
 (2, 'Broken', 1, 'free', 'Hotspot', 'Unlimited', 1, 'Days', 'r1', '', 1, 'yes', '');
INSERT INTO tbl_customers (id, username, password, fullname, phonenumber, balance, service_type, pppoe_password, created_by) VALUES
 (1, 'budi', 'pw1', 'Budi', '0812', 2500.00, 'PPPoE', 'ppw', 1);
INSERT INTO tbl_user_recharges (id, customer_id, username, plan_id, namebp, recharged_on, recharged_time, expiration, time, status, routers, type) VALUES
 (1, 1, 'budi', 1, 'Gold', '2024-01-01', '08:00:00', '2024-02-01', '23:59:00', 'on', 'r1', 'PPPOE'),
 (2, 99, 'gone', 1, 'Gold', '2024-01-01', '08:00:00', '2024-02-01', '23:59:00', 'on', 'r1', 'PPPOE');
INSERT INTO tbl_transactions (id, invoice, username, user_id, plan_name, price, recharged_on, expiration, time, method, routers, type) VALUES
 (1, 'INV1', 'budi', 1, 'Gold', '10.000', '2024-01-01', '2024-02-01', '23:59:00', 'cash', 'r1', 'PPPOE'),
 (3, 'INV3', 'gone', 99, 'Gold', '5.000', '2024-01-01', '2024-02-01', '23:59:00', 'cash', 'r1', 'PPPOE'),
 (2, 'INV2', 'budi', 1, 'Gold', 'oops', '2024-01-01', '2024-02-01', '23:59:00', 'cash', 'r1', 'PPPOE');`
	if _, err := my.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	if _, err := my.Exec(seed); err != nil {
		t.Fatal(err)
	}

	lite, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	if err := db.Migrate(lite); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Jakarta")
	opts := Options{Key: make([]byte, 32), Loc: loc}
	rep, err := Run(ctx, my, lite, opts)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	rep.Print(&b)
	t.Log(b.String())

	count := func(q string) (n int) { lite.QueryRow(q).Scan(&n); return }
	for q, want := range map[string]int{
		"SELECT count(*) FROM admins":       1, // 'bad' has no sha1
		"SELECT count(*) FROM plans":        1, // 'Broken' has an unparsable price
		"SELECT count(*) FROM transactions": 2,
		"SELECT count(*) FROM transactions WHERE customer_id IS NULL AND username = 'gone'":  1,
		"SELECT count(*) FROM subscriptions":                                                 1,
		"SELECT count(*) FROM pools":                                                         1,
		"SELECT balance FROM customers":                                                      2500,
		"SELECT price FROM plans":                                                            10000,
		"SELECT port FROM routers":                                                           8729,
		"SELECT count(*) FROM settings WHERE key = 'CompanyName' AND value = 'ACME'":         1,
		"SELECT count(*) FROM admins WHERE password_hash = '!' AND length(legacy_sha1) = 40": 1,
		"SELECT expires_at - started_at FROM subscriptions":                                  31*24*3600 + 15*3600 + 59*60,
	} {
		if got := count(q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
	if _, err := Run(ctx, my, lite, opts); err == nil {
		t.Error("second import into a non-empty target should be refused")
	}
	opts.Force = true
	if _, err := Run(ctx, my, lite, opts); err != nil {
		t.Errorf("--force: %v", err)
	}
}

// TestOrphans runs the subscription/transaction steps against a stand-in "MySQL" (SQLite) holding rows of a deleted customer.
func TestOrphans(t *testing.T) {
	ctx := context.Background()
	my, _ := sql.Open("sqlite", ":memory:")
	my.SetMaxOpenConns(1)
	defer my.Close()
	if _, err := my.Exec(`CREATE TABLE tbl_user_recharges (id, customer_id, plan_id, recharged_on, recharged_time, expiration, time, status, method, routers, type, admin_id);
		INSERT INTO tbl_user_recharges VALUES (1, 99, 1, '2024-01-01', '08:00:00', '2024-02-01', '23:59:00', 'on', '', '', 'Hotspot', 0);
		CREATE TABLE tbl_transactions (id, invoice, username, user_id, plan_name, price, recharged_on, recharged_time, expiration, time, method, routers, type, note, admin_id);
		INSERT INTO tbl_transactions VALUES (1, 'INV1', 'gone', 99, 'Gold', '5000', '2024-01-01', '08:00:00', '2024-02-01', '23:59:00', 'cash', '', 'Hotspot', '', 0);`); err != nil {
		t.Fatal(err)
	}
	lite, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	if err := db.Migrate(lite); err != nil {
		t.Fatal(err)
	}
	tx, _ := lite.BeginTx(ctx, nil)
	defer tx.Rollback()
	loc, _ := time.LoadLocation("Asia/Jakarta")
	m := &imp{my: my, tx: tx, ctx: ctx, o: Options{Loc: loc}, rep: &Report{}, plans: map[string]int64{}, admins: map[int64]bool{}, custIDs: map[int64]bool{}}
	if err := m.subscription(); err != nil {
		t.Fatal(err)
	}
	if err := m.transaction(); err != nil {
		t.Fatal(err)
	}
	sub, trx := m.rep.Tables[0], m.rep.Tables[1]
	if len(sub.Skips) != 1 || !strings.Contains(sub.Skips[0], "customer deleted in old system") {
		t.Errorf("subscription skips: %v", sub.Skips)
	}
	if trx.Loaded != 1 || len(trx.Notes) != 1 || !strings.HasPrefix(trx.Notes[0], "1 ") {
		t.Errorf("transactions: loaded %d notes %v skips %v", trx.Loaded, trx.Notes, trx.Skips)
	}
}
