// Command seed fills the lab's gobill database (run once, before gobill starts, on a fresh volume).
// It uses SQL plus the app's own secret package, so routers, NAS and customer secrets are
// encrypted with the key stored next to the database (created here when missing).
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

func main() {
	path := flag.String("db", "/data/gobill.db", "database path")
	adminPass := flag.String("admin-pass", "", "gobill admin password")
	rHost := flag.String("router-host", "", "CHR API host")
	rUser := flag.String("router-user", "gobill-api", "CHR API user")
	rPass := flag.String("router-pass", "", "CHR API password")
	nasIP := flag.String("nas-ip", "", "source IP of the CHR as gobill sees it")
	nasSecret := flag.String("nas-secret", "", "RADIUS shared secret")
	flag.Parse()

	conn, err := db.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		log.Fatal(err)
	}
	key, _, err := secret.LoadKey("", *path+".key")
	if err != nil {
		log.Fatal(err)
	}
	must := func(_ any, err error) {
		if err != nil {
			log.Fatal(err)
		}
	}
	seal := func(s string) []byte {
		b, err := secret.Seal(key, []byte(s))
		if err != nil {
			log.Fatal(err)
		}
		return b
	}
	hash := func(s string) string {
		b, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.MinCost)
		if err != nil {
			log.Fatal(err)
		}
		return string(b)
	}
	x := func(q string, a ...any) { must(conn.Exec(q, a...)) }

	x(`INSERT INTO settings(key,value) VALUES('language','english')`) // the test greps English flash messages
	x(`INSERT INTO admins(username,fullname,password_hash,role) VALUES('admin','Lab Admin',?,'SuperAdmin')`, hash(*adminPass))
	x(`INSERT INTO routers(id,name,host,port,username,password_enc,description) VALUES(1,'lab-chr',?,8728,?,?,'RouterOS CHR in the lab')`,
		*rHost, *rUser, seal(*rPass))
	x(`INSERT INTO nas(name,ip,secret_enc,description) VALUES('lab-chr',?,?,'CHR PPPoE server')`, *nasIP, seal(*nasSecret))
	x(`INSERT INTO bandwidths(id,name,rate_down,rate_down_unit,rate_up,rate_up_unit,burst) VALUES
		(1,'lab-5M',5,'Mbps',5,'Mbps',''),
		(2,'lab-10M-burst',10,'Mbps',10,'Mbps','20M/20M 15M/15M 8'),
		(3,'lab-20M',20,'Mbps',20,'Mbps','')`)
	x(`INSERT INTO pools(id,name,local_ip,range_ip,router_id) VALUES(1,'gb-pool','10.99.0.1','10.99.0.10-10.99.0.250',1)`)
	x(`INSERT INTO plans(id,name,type,price,validity,validity_unit,bandwidth_id,router_id,pool_id,device) VALUES
		(1,'lab-g5','PPPoE',100000,30,'Days',1,1,1,'MikrotikPppoe'),
		(2,'lab-g10','PPPoE',150000,30,'Days',2,1,1,'MikrotikPppoe')`)
	x(`INSERT INTO plans(id,name,type,price,validity,validity_unit,bandwidth_id,router_id,pool_id,device) VALUES
		(3,'lab-rad','PPPoE',200000,30,'Days',3,NULL,NULL,'Radius')`)

	now := time.Now().Unix()
	add := func(id int, user, pass string, plan int, router any) {
		x(`INSERT INTO customers(id,username,password_hash,fullname,email,service_type,secret_enc,created_by) VALUES(?,?,?,?,?,'PPPoE',?,1)`,
			id, user, hash(pass), "Lab "+user, user+"@lab.test", seal(pass))
		x(`INSERT INTO subscriptions(customer_id,plan_id,router_id,type,started_at,expires_at,method,admin_id) VALUES(?,?,?,'PPPoE',?,?,'lab',1)`,
			id, plan, router, now, now+30*86400)
	}
	// cust01..13: API-synced plans (cust01..12 also exist on the CHR, cust13 does not).
	for i := 1; i <= 13; i++ {
		plan := 1 + i%2
		add(i, fmt.Sprintf("cust%02d", i), fmt.Sprintf("pw-cust%02d", i), plan, 1)
	}
	// rad01, rad02: RADIUS-only plan, never on the CHR's local secrets; the lab clients dial these.
	add(14, "rad01", "pw-rad01", 3, nil)
	add(15, "rad02", "pw-rad02", 3, nil)
	log.Printf("seeded %s", *path)
}
