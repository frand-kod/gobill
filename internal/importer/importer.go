// Package importer copies a PHPNuxBill MySQL database into the SQLite schema.
package importer

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/secret"
)

// Options configure Run.
type Options struct {
	Key    []byte         // secret.LoadKey result
	Loc    *time.Location // timezone of the old date/time columns
	DryRun bool           // roll back at the end
	Force  bool           // wipe a non-empty target first
	// Notifications is the PHP uploads/notifications.json (message templates); empty = skip.
	Notifications string
}

// Table is the per-table result.
type Table struct {
	Name         string
	Read, Loaded int
	Skips        []string
	Notes        []string
}

func (t *Table) skip(id any, why any) { t.Skips = append(t.Skips, fmt.Sprintf("id %v: %v", id, why)) }

// Report is the whole import result.
type Report struct{ Tables []*Table }

// Print writes the report; at most 10 skip reasons per table.
func (r *Report) Print(w io.Writer) {
	for _, t := range r.Tables {
		fmt.Fprintf(w, "%-14s read %-6d imported %-6d skipped %d\n", t.Name, t.Read, t.Loaded, len(t.Skips))
		for i, s := range t.Skips {
			if i == 10 {
				fmt.Fprintf(w, "    ... %d more\n", len(t.Skips)-10)
				break
			}
			fmt.Fprintf(w, "    skip %s\n", s)
		}
		for _, n := range t.Notes {
			fmt.Fprintf(w, "    note %s\n", n)
		}
	}
	fmt.Fprintln(w, "not imported yet: coupons, ODP, inbox")
}

type row []string

func (r row) i(n int) int64 { v, _ := strconv.ParseInt(strings.TrimSpace(r[n]), 10, 64); return v }

type imp struct {
	my  *sql.DB
	tx  *sql.Tx
	ctx context.Context
	o   Options
	rep *Report
	// old name/id lookups
	routers, pools, plans, customers map[string]int64
	admins                           map[int64]bool
	custIDs                          map[int64]bool
}

// missing turns an absent table/column error (MySQL or SQLite text) into a report note; "" = a real error.
func missing(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "doesn't exist"), strings.Contains(s, "no such table"):
		return "missing or empty in source, skipped"
	case strings.Contains(s, "Unknown column"), strings.Contains(s, "no such column"):
		return "column missing, skipped: " + s
	}
	return ""
}

// each runs q on the source and calls fn with every row as strings ("" for NULL).
func (m *imp) each(t *Table, q string, n int, fn func(row)) error {
	rows, err := m.my.QueryContext(m.ctx, q)
	if err != nil {
		if why := missing(err); why != "" {
			t.Notes = append(t.Notes, why)
			return nil
		}
		return fmt.Errorf("%s: %w", t.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		ns := make([]sql.NullString, n)
		dst := make([]any, n)
		for i := range ns {
			dst[i] = &ns[i]
		}
		if err := rows.Scan(dst...); err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		r := make(row, n)
		for i := range ns {
			r[i] = ns[i].String
		}
		t.Read++
		fn(r)
	}
	return rows.Err()
}

func (m *imp) put(t *Table, id any, q string, args ...any) bool {
	if _, err := m.tx.ExecContext(m.ctx, q, args...); err != nil {
		t.skip(id, err)
		return false
	}
	t.Loaded++
	return true
}

func nz(s string) any {
	if s == "" || s == "0" {
		return nil
	}
	return s
}

func orNil(id int64, ok bool) any {
	if ok {
		return id
	}
	return nil
}

var dataTables = []string{"activity_logs", "vouchers", "transactions", "subscriptions", "customers",
	"plans", "pools", "bandwidths", "routers", "nas", "admins", "settings"}

// Run imports everything in one SQLite transaction.
func Run(ctx context.Context, my, lite *sql.DB, o Options) (*Report, error) {
	tx, err := lite.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, t := range dataTables {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		if !o.Force {
			return nil, fmt.Errorf("target is not empty (%s has %d rows); use --force to wipe it", t, n)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return nil, err
		}
	}
	m := &imp{my: my, tx: tx, ctx: ctx, o: o, rep: &Report{},
		routers: map[string]int64{}, pools: map[string]int64{}, plans: map[string]int64{},
		customers: map[string]int64{}, admins: map[int64]bool{}, custIDs: map[int64]bool{}}
	for _, step := range []func() error{m.settings, m.notifications, m.admin, m.router, m.bandwidth, m.pool, m.plan,
		m.customer, m.fields, m.subscription, m.transaction, m.voucher, m.logs, m.nas} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	if o.DryRun {
		return m.rep, nil
	}
	return m.rep, tx.Commit()
}

func (m *imp) table(name string) *Table {
	t := &Table{Name: name}
	m.rep.Tables = append(m.rep.Tables, t)
	return t
}

func (m *imp) settings() error {
	t := m.table("settings")
	return m.each(t, "SELECT setting, COALESCE(value,'') FROM tbl_appconfig ORDER BY id", 2, func(r row) {
		m.put(t, r[0], "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", r[0], r[1])
	})
}

var sha1Hex = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func (m *imp) admin() error {
	t := m.table("admins")
	err := m.each(t, `SELECT id, username, fullname, password, phone, email, city, user_type, status, root,
		COALESCE(last_login,''), COALESCE(creationdate,'') FROM tbl_users ORDER BY id`, 12, func(r row) {
		if !sha1Hex.MatchString(r[3]) {
			t.skip(r[0], "password is not a sha1 hash")
			return
		}
		last, created := any(nil), time.Now().Unix()
		if v, err := ParseWhen(r[10], "", m.o.Loc); err == nil {
			last = v
		}
		if v, err := ParseWhen(r[11], "", m.o.Loc); err == nil {
			created = v
		}
		if m.put(t, r[0], `INSERT INTO admins (id, username, fullname, password_hash, legacy_sha1, role, status, email, phone, city, last_login_at, created_at)
			VALUES (?, ?, ?, '!', ?, ?, ?, ?, ?, ?, ?, ?)`, r.i(0), r[1], r[2], strings.ToLower(r[3]), r[7], r[8], r[5], r[4], r[6], last, created) {
			m.admins[r.i(0)] = true
		}
	})
	if err != nil {
		return err
	}
	// Second pass for sub-accounts, once every parent exists.
	return m.each(&Table{Name: "admins"}, "SELECT id, root FROM tbl_users", 2, func(r row) {
		if m.admins[r.i(0)] && m.admins[r.i(1)] {
			m.tx.ExecContext(m.ctx, "UPDATE admins SET root_id = ? WHERE id = ?", r.i(1), r.i(0))
		}
	})
}

func (m *imp) router() error {
	t := m.table("routers")
	return m.each(t, "SELECT id, name, ip_address, username, password, COALESCE(description,''), enabled FROM tbl_routers ORDER BY id", 7, func(r row) {
		host, port := r[2], "8728"
		if h, p, err := net.SplitHostPort(r[2]); err == nil {
			host, port = h, p
		}
		enc, err := secret.Seal(m.o.Key, []byte(r[4]))
		if err != nil {
			t.skip(r[0], err)
			return
		}
		if m.put(t, r[0], `INSERT INTO routers (id, name, host, port, username, password_enc, description, enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.i(0), r[1], host, port, r[3], enc, r[5], r.i(6) != 0) {
			m.routers[r[1]] = r.i(0)
		}
	})
}

func (m *imp) bandwidth() error {
	t := m.table("bandwidths")
	return m.each(t, "SELECT id, name_bw, rate_down, rate_down_unit, rate_up, rate_up_unit, COALESCE(burst,'') FROM tbl_bandwidth ORDER BY id", 7, func(r row) {
		m.put(t, r[0], `INSERT INTO bandwidths (id, name, rate_down, rate_down_unit, rate_up, rate_up_unit, burst) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.i(0), r[1], r.i(2), r[3], r.i(4), r[5], r[6])
	})
}

func (m *imp) pool() error {
	t := m.table("pools")
	return m.each(t, "SELECT id, pool_name, COALESCE(local_ip,''), range_ip, routers FROM tbl_pool ORDER BY id", 5, func(r row) {
		rid, ok := m.routers[r[4]]
		if !ok {
			t.skip(r[0], fmt.Sprintf("router %q not imported", r[4]))
			return
		}
		if m.put(t, r[0], `INSERT INTO pools (id, name, local_ip, range_ip, router_id) VALUES (?, ?, ?, ?, ?)`, r.i(0), r[1], r[2], r[3], rid) {
			m.pools[fmt.Sprint(rid, "/", r[1])] = r.i(0)
		}
	})
}

var devices = map[string]bool{"MikrotikHotspot": true, "MikrotikPppoe": true, "Dummy": true, "Radius": true}

func (m *imp) plan() error {
	t := m.table("plans")
	type fix struct{ id, expired int64 }
	var fixes []fix
	err := m.each(t, `SELECT id, name_plan, id_bw, price, type, COALESCE(typebp,''), COALESCE(limit_type,''), COALESCE(time_limit,0),
		COALESCE(time_unit,''), COALESCE(data_limit,0), COALESCE(data_unit,''), validity, validity_unit, COALESCE(shared_users,0),
		routers, is_radius, COALESCE(pool,''), plan_expired, expired_date, enabled, COALESCE(prepaid,'yes'), device,
		COALESCE(on_login,''), COALESCE(on_logout,'') FROM tbl_plans ORDER BY id`, 24, func(r row) {
		price, err := ParsePrice(r[3])
		if err != nil {
			t.skip(r[0], fmt.Sprintf("price %q: %v", r[3], err))
			return
		}
		typ, err := PlanType(r[4])
		if err != nil {
			t.skip(r[0], err)
			return
		}
		radius := r.i(15) != 0
		device := r[21]
		switch {
		case radius:
			device = "Radius"
		case !devices[device] && typ == "Hotspot":
			device = "MikrotikHotspot"
		case !devices[device] && typ == "PPPoE":
			device = "MikrotikPppoe"
		case typ == "Balance":
			device = ""
		}
		var bw, router, pool any
		if typ != "Balance" {
			bw = r.i(2)
			if !radius {
				rid, ok := m.routers[r[14]]
				if !ok {
					t.skip(r[0], fmt.Sprintf("router %q not imported", r[14]))
					return
				}
				router = rid
				if pid, ok := m.pools[fmt.Sprint(rid, "/", r[16])]; ok {
					pool = pid
				}
			}
		}
		limited := r[5] == "Limited"
		var lt, tl, tu, dl, du any
		if limited {
			lt, tl, tu, dl, du = nz(r[6]), nz(r[7]), nz(r[8]), nz(r[9]), nz(r[10])
		}
		var day any
		if r[12] == "Period" && r.i(18) >= 1 && r.i(18) <= 31 {
			day = r.i(18)
		}
		billing := "prepaid"
		if r[20] == "no" {
			billing = "postpaid"
		}
		if m.put(t, r[0], `INSERT INTO plans (id, name, type, billing, price, validity, validity_unit, limited, limit_type, time_limit, time_unit,
			data_limit, data_unit, shared_users, bandwidth_id, router_id, pool_id, billing_day, on_login, on_logout, device, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.i(0), r[1], typ, billing, price, r.i(11), r[12], limited, lt, tl, tu, dl, du,
			nz(fmt.Sprint(r.i(13))), bw, router, pool, day, r[22], r[23], device, r.i(19) != 0) {
			m.plans[r[1]] = r.i(0)
			if r.i(17) > 0 {
				fixes = append(fixes, fix{r.i(0), r.i(17)})
			}
		}
	})
	if err != nil {
		return err
	}
	// plan_expired points at another plan, so set it after all plans exist.
	for _, f := range fixes {
		if _, err := m.tx.ExecContext(m.ctx, "UPDATE plans SET expired_plan_id = ? WHERE id = ? AND EXISTS (SELECT 1 FROM plans WHERE id = ?)", f.expired, f.id, f.expired); err != nil {
			return err
		}
	}
	return nil
}

func (m *imp) customer() error {
	t := m.table("customers")
	return m.each(t, `SELECT id, username, password, fullname, COALESCE(address,''), COALESCE(phonenumber,''), COALESCE(email,''),
		balance, COALESCE(service_type,'Others'), pppoe_username, pppoe_password, pppoe_ip, auto_renewal, status, created_by,
		COALESCE(created_at,''), COALESCE(last_login,'') FROM tbl_customers ORDER BY id`, 17, func(r row) {
		hash, err := bcrypt.GenerateFromPassword([]byte(r[2]), bcrypt.DefaultCost)
		if err != nil { // passwords over 72 bytes
			t.skip(r[0], err)
			return
		}
		pass := r[2]
		if r[8] == "PPPoE" && r[10] != "" {
			pass = r[10]
		}
		enc, err := secret.Seal(m.o.Key, []byte(pass))
		if err != nil {
			t.skip(r[0], err)
			return
		}
		bal, err := ParsePrice(r[7])
		if err != nil || bal < 0 {
			bal = 0
			t.Notes = append(t.Notes, fmt.Sprintf("id %s: balance %q set to 0", r[0], r[7]))
		}
		svc := r[8]
		if svc != "Hotspot" && svc != "PPPoE" {
			svc = "Others"
		}
		email, phone := r[6], r[5]
		if email == "1" {
			email = ""
		}
		if phone == "0" {
			phone = ""
		}
		created, last := time.Now().Unix(), any(nil)
		if v, err := ParseWhen(r[15], "", m.o.Loc); err == nil {
			created = v
		}
		if v, err := ParseWhen(r[16], "", m.o.Loc); err == nil {
			last = v
		}
		if m.put(t, r[0], `INSERT INTO customers (id, username, password_hash, fullname, address, phone, email, balance, service_type,
			pppoe_username, pppoe_ip, secret_enc, auto_renewal, status, created_by, created_at, last_login_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.i(0), r[1], string(hash), r[3], r[4], phone, email, bal, svc, r[9], r[11], enc, r.i(12) != 0, r[13],
			orNil(r.i(14), m.admins[r.i(14)]), created, last) {
			m.customers[r[1]] = r.i(0)
			m.custIDs[r.i(0)] = true
		}
	})
}

func (m *imp) routerID(name string) (any, bool) {
	if id, ok := m.routers[name]; ok {
		return id, true
	}
	return nil, name == "" || strings.EqualFold(name, "radius")
}

func (m *imp) subscription() error {
	t := m.table("subscriptions")
	return m.each(t, `SELECT id, customer_id, plan_id, recharged_on, recharged_time, expiration, time, status, method, routers, type, admin_id
		FROM tbl_user_recharges ORDER BY id`, 12, func(r row) {
		typ, err := PlanType(r[10])
		if err != nil || typ == "Balance" {
			t.skip(r[0], "unsupported type "+r[10])
			return
		}
		if !m.custIDs[r.i(1)] {
			t.skip(r[0], "customer deleted in old system")
			return
		}
		rid, ok := m.routerID(r[9])
		if !ok {
			t.skip(r[0], fmt.Sprintf("router %q not imported", r[9]))
			return
		}
		start, err1 := ParseWhen(r[3], r[4], m.o.Loc)
		exp, err2 := ParseWhen(r[5], r[6], m.o.Loc)
		if err1 != nil || err2 != nil {
			t.skip(r[0], fmt.Sprint("bad date: ", err1, err2))
			return
		}
		start = min(start, exp)
		m.put(t, r[0], `INSERT INTO subscriptions (id, customer_id, plan_id, router_id, type, started_at, expires_at, status, method, admin_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.i(0), r.i(1), r.i(2), rid, typ, start, exp, SubStatus(r[7]), r[8],
			orNil(r.i(11), m.admins[r.i(11)]))
	})
}

func (m *imp) transaction() error {
	t := m.table("transactions")
	orphans := 0
	err := m.each(t, `SELECT id, invoice, username, user_id, plan_name, price, recharged_on, recharged_time, expiration, time,
		method, routers, type, note, admin_id FROM tbl_transactions ORDER BY id`, 15, func(r row) {
		price, err := ParsePrice(r[5])
		if err != nil {
			t.skip(r[0], fmt.Sprintf("price %q: %v", r[5], err))
			return
		}
		typ, err := PlanType(r[12])
		if err != nil {
			t.skip(r[0], err)
			return
		}
		cid := r.i(3)
		if cid == 0 {
			cid = m.customers[r[2]]
		}
		start, err1 := ParseWhen(r[6], r[7], m.o.Loc)
		end, err2 := ParseWhen(r[8], r[9], m.o.Loc)
		if err1 != nil || err2 != nil {
			t.skip(r[0], fmt.Sprint("bad date: ", err1, err2))
			return
		}
		pid, ok := m.plans[r[4]]
		var cust any = cid
		if !m.custIDs[cid] {
			cust = nil
			orphans++
		}
		m.put(t, r[0], `INSERT INTO transactions (id, invoice, customer_id, plan_id, username, plan_name, router_name, type, price, method, note,
			admin_id, created_at, period_start, period_end) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.i(0), r[1], cust, orNil(pid, ok), r[2], r[4], r[11], typ, price, r[10], r[13], orNil(r.i(14), m.admins[r.i(14)]), start, start, max(start, end))
	})
	if orphans > 0 {
		t.Notes = append(t.Notes, fmt.Sprintf("%d transactions kept without a customer (customer deleted in old system)", orphans))
	}
	return err
}

func (m *imp) voucher() error {
	t := m.table("vouchers")
	return m.each(t, `SELECT id, code, id_plan, status, user, COALESCE(used_date,''), COALESCE(created_at,''), generated_by FROM tbl_voucher ORDER BY id`, 8, func(r row) {
		created, err := ParseWhen(r[6], "", m.o.Loc)
		if err != nil {
			created = time.Now().Unix()
		}
		status, usedAt, usedBy := "unused", any(nil), any(nil)
		if r[3] != "0" && !strings.EqualFold(r[3], "not used") && r[3] != "" {
			status, usedAt = "used", created
			if v, err := ParseWhen(r[5], "", m.o.Loc); err == nil {
				usedAt = v
			}
			if id, ok := m.customers[r[4]]; ok {
				usedBy = id
			}
		}
		m.put(t, r[0], `INSERT INTO vouchers (id, code, plan_id, status, used_by, used_at, generated_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.i(0), r[1], r.i(2), status, usedBy, usedAt, orNil(r.i(7), m.admins[r.i(7)]), created)
	})
}

func (m *imp) logs() error {
	t := m.table("activity_logs")
	return m.each(t, "SELECT id, COALESCE(date,''), type, description, userid, ip FROM tbl_logs ORDER BY id", 6, func(r row) {
		at, err := ParseWhen(r[1], "", m.o.Loc)
		if err != nil {
			at = 0
		}
		actor := "admin"
		if r.i(4) == 0 {
			actor = "system"
		}
		m.put(t, r[0], `INSERT INTO activity_logs (id, actor_type, actor_id, action, description, ip, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.i(0), actor, r.i(4), r[2], r[3], r[5], at)
	})
}

func (m *imp) nas() error {
	t := m.table("nas")
	return m.each(t, "SELECT id, nasname, COALESCE(NULLIF(shortname,''), nasname), secret, COALESCE(description,'') FROM nas ORDER BY id", 5, func(r row) {
		enc, err := secret.Seal(m.o.Key, []byte(r[3]))
		if err != nil {
			t.skip(r[0], err)
			return
		}
		m.put(t, r[0], `INSERT INTO nas (id, name, ip, secret_enc, description) VALUES (?, ?, ?, ?, ?)`, r.i(0), r[2], r[1], enc, r[4])
	})
}

// Timezone returns the old appconfig timezone, or Asia/Jakarta.
func Timezone(ctx context.Context, my *sql.DB) string {
	var tz string
	if my.QueryRowContext(ctx, "SELECT value FROM tbl_appconfig WHERE setting = 'timezone'").Scan(&tz) != nil || tz == "" {
		return "Asia/Jakarta"
	}
	return tz
}
