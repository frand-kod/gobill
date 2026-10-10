package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// TestImportFromJSON: a backup without tbl_transactions and tbl_logs imports the rest and reports the two as missing.
func TestImportFromJSON(t *testing.T) {
	ctx := context.Background()
	fixture := `{
	"tbl_appconfig": [{"id":"1","setting":"timezone","value":"Asia/Jakarta"}, {"id":"2","setting":"CompanyName","value":"ACME"}],
	"tbl_routers": [{"id":"1","name":"r1","ip_address":"10.0.0.1","username":"api","password":"rpw","description":null,"enabled":"1"}],
	"tbl_bandwidth": [{"id":"1","name_bw":"5M","rate_down":"5","rate_down_unit":"Mbps","rate_up":"5","rate_up_unit":"Mbps","burst":null}],
	"tbl_plans": [{"id":"1","name_plan":"Gold","id_bw":"1","price":"10000","type":"PPPOE","typebp":"Unlimited","limit_type":null,
		"time_limit":null,"time_unit":null,"data_limit":null,"data_unit":null,"validity":"1","validity_unit":"Months",
		"shared_users":"1","routers":"r1","is_radius":"0","pool":null,"plan_expired":"0","expired_date":"0","enabled":"1",
		"prepaid":"yes","device":"MikrotikPppoe","on_login":null,"on_logout":null}],
	"tbl_customers": [{"id":"1","username":"budi","password":"pw1","fullname":"Budi","address":null,"phonenumber":"0812345678",
		"email":"budi@example.com","balance":"2500.00","service_type":"PPPoE","pppoe_username":"budi","pppoe_password":"ppw",
		"pppoe_ip":null,"auto_renewal":"0","status":"Active","created_by":"0","created_at":"2024-01-01 00:00:00","last_login":null}],
	"tbl_user_recharges": [{"id":"1","customer_id":"1","plan_id":"1","recharged_on":"2024-01-01","recharged_time":"08:00:00",
		"expiration":"2024-02-01","time":"23:59:00","status":"on","method":"cash","routers":"r1","type":"PPPOE","admin_id":"0"}],
	"tbl_transactions": []
}`
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	my, err := OpenJSON(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer my.Close()
	lite, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	if err := db.Migrate(lite); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Jakarta")
	rep, err := Run(ctx, my, lite, Options{Key: make([]byte, 32), Loc: loc})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	rep.Print(&b)
	t.Log(b.String())

	count := func(q string) (n int) { lite.QueryRow(q).Scan(&n); return }
	if n := count("SELECT count(*) FROM customers WHERE phone = '0812345678'"); n != 1 {
		t.Errorf("customer with phone 0812345678: %d", n)
	}
	if n := count("SELECT count(*) FROM subscriptions"); n != 1 {
		t.Errorf("subscriptions = %d", n)
	}
	for _, name := range []string{"transactions", "activity_logs"} {
		var missing bool
		for _, tb := range rep.Tables {
			if tb.Name == name && len(tb.Notes) == 1 && strings.Contains(tb.Notes[0], "missing or empty in source") {
				missing = true
			}
		}
		if !missing {
			t.Errorf("%s not reported as missing", name)
		}
	}
}
