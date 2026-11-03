// Command owrtctl is the gobill side of the OpenWrt lab (lab/openwrt.sh). It works on the lab database
// directly, with the app's own packages:
//
//	owrtctl -db PATH seed -admin-pass P -nas-secret S   fresh lab data (run before gobill starts)
//	owrtctl -db PATH coa USER                           CoA per open session, Disconnect on failure (as the FUP job)
//	owrtctl -db PATH sql STATEMENT                      run one statement
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
)

func main() {
	path := flag.String("db", "", "database path")
	adminPass := flag.String("admin-pass", "", "seed: gobill admin password")
	nasSecret := flag.String("nas-secret", "", "seed: RADIUS secret of the OpenWrt NAS")
	flag.Parse()
	if *path == "" || flag.NArg() < 1 {
		log.Fatal("usage: owrtctl -db PATH seed|coa USER|sql STATEMENT")
	}
	conn, err := db.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ctx := context.Background()
	x := func(q string, a ...any) {
		if _, err := conn.Exec(q, a...); err != nil {
			log.Fatalf("%s: %v", q, err)
		}
	}

	switch flag.Arg(0) {
	case "sql":
		x(flag.Arg(1))
	case "coa":
		key, _, err := secret.LoadKey("", *path+".key")
		if err != nil {
			log.Fatal(err)
		}
		q := db.New(conn)
		ss, err := q.ListOpenRadiusSessionsByUser(ctx, flag.Arg(1))
		if err != nil {
			log.Fatal(err)
		}
		for _, s := range ss {
			err := radius.CoA(ctx, q, key, radius.CoAPort, s, "", time.Now())
			fmt.Println("coa", err)
			if err != nil {
				fmt.Println("disconnect", radius.Disconnect(ctx, q, key, radius.CoAPort, s))
			}
		}
	case "seed":
		if err := db.Migrate(conn); err != nil {
			log.Fatal(err)
		}
		key, _, err := secret.LoadKey("", *path+".key")
		if err != nil {
			log.Fatal(err)
		}
		seal := func(s string) []byte {
			b, err := secret.Seal(key, []byte(s))
			if err != nil {
				log.Fatal(err)
			}
			return b
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*adminPass), bcrypt.MinCost)
		if err != nil {
			log.Fatal(err)
		}
		x(`INSERT INTO settings(key,value) VALUES('language','english')`)
		x(`INSERT INTO admins(username,fullname,password_hash,role) VALUES('admin','Lab Admin',?,'SuperAdmin')`, string(hash))
		// gobill sees the NAS behind podman's NAT at the host address, which differs per machine
		x(`INSERT INTO nas(name,ip,secret_enc,description,type) VALUES('lab-owrt','0.0.0.0/0',?,'OpenWrt uspot','wispr')`, seal(*nasSecret))
		x(`INSERT INTO bandwidths(id,name,rate_down,rate_down_unit,rate_up,rate_up_unit,burst) VALUES
			(1,'ow-4M',4,'Mbps',1,'Mbps',''), (2,'ow-1M',1,'Mbps',512,'Kbps',''), (3,'ow-20M',20,'Mbps',20,'Mbps','')`)
		x(`INSERT INTO plans(id,name,type,price,validity,validity_unit,bandwidth_id,device) VALUES
			(1,'ow-speed','Hotspot',5000,30,'Days',1,'Radius')`)
		x(`INSERT INTO plans(id,name,type,price,validity,validity_unit,bandwidth_id,device,limited,limit_type,data_limit,data_unit) VALUES
			(2,'ow-quota','Hotspot',5000,30,'Days',3,'Radius',1,'Data_Limit',5,'MB')`)
		now := time.Now().Unix()
		for i, c := range []struct {
			user string
			plan int
		}{{"hs01", 1}, {"hs02", 2}, {"hs03", 1}} {
			id := i + 1
			x(`INSERT INTO customers(id,username,password_hash,fullname,service_type,secret_enc,created_by) VALUES(?,?,'x',?,'Hotspot',?,1)`,
				id, c.user, "Lab "+c.user, seal("pw-"+c.user))
			x(`INSERT INTO subscriptions(customer_id,plan_id,type,started_at,expires_at,method,admin_id) VALUES(?,?,'Hotspot',?,?,'lab',1)`,
				id, c.plan, now, now+30*86400)
		}
		// hs03 is isolated: no active plan, isolir on, isolation running
		x(`UPDATE subscriptions SET started_at=?, expires_at=?, status='expired' WHERE customer_id=3`, now-86400, now-60)
		if _, err := isolir.EnsurePlan(ctx, db.New(conn)); err != nil {
			log.Fatal(err)
		}
		x(`INSERT INTO settings(key,value) VALUES('isolir_enabled','yes')`)
		x(`INSERT INTO isolations(customer_id,started_at,ends_at) VALUES(3,?,?)`, now, now+86400)
		log.Printf("seeded %s", *path)
	default:
		log.Fatalf("unknown command %q", flag.Arg(0))
	}
}
