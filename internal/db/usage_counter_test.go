package db

import (
	"context"
	"path/filepath"
	"testing"
)

// Migration 0041 fills the counters of active subscriptions with the sum quota used before (LogUsage),
// and RecountSubscriptionUsage gives the same numbers again.
func TestUsageCounterBackfill(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	q := New(conn)
	if _, err := conn.Exec(`
		INSERT INTO customers (id, username, pppoe_username, password_hash, fullname, service_type, status) VALUES (1, 'alice', 'alice-ppp', 'h', 'A', 'Hotspot', 'Active');
		INSERT INTO plans (id, name, type, price, validity, validity_unit, device) VALUES (1, 'P', 'Balance', 1, 30, 'Days', 'Radius');
		INSERT INTO subscriptions (id, customer_id, plan_id, type, started_at, expires_at, status) VALUES (1, 1, 1, 'Hotspot', 1000, 9000, 'active');
		INSERT INTO subscriptions (id, customer_id, plan_id, type, started_at, expires_at, status) VALUES (2, 1, 1, 'Hotspot', 100, 900, 'expired');
		INSERT INTO radius_sessions (session_id, username, nas_ip, started_at, updated_at, stopped_at, input_octets, output_octets) VALUES
			('before', 'alice', '1.1.1.1', 999, 1500, 1500, 5, 5),
			('u', 'alice', '1.1.1.1', 1000, 1100, 1100, 10, 20),
			('p', 'alice-ppp', '1.1.1.1', 2000, 2300, NULL, 30, 40),
			('other', 'bob', '1.1.1.1', 2000, 2300, NULL, 99, 99)`); err != nil {
		t.Fatal(err)
	}
	// back to version 40 without the columns, so 0041 runs on this data
	if _, err := conn.Exec(`ALTER TABLE subscriptions DROP COLUMN used_in_octets; ALTER TABLE subscriptions DROP COLUMN used_out_octets;
		ALTER TABLE subscriptions DROP COLUMN used_seconds; PRAGMA user_version = 40`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		s, err := q.GetSubscription(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := q.GetCustomer(ctx, 1)
		bytes, secs, err := LogUsage(ctx, q, c, s.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		if s.UsedInOctets != 40 || s.UsedOutOctets != 60 || s.UsedSeconds != 400 || bytes != 100 || secs != 400 {
			t.Fatalf("%s: used %d/%d/%d s, log %d/%d s", when, s.UsedInOctets, s.UsedOutOctets, s.UsedSeconds, bytes, secs)
		}
		if old, _ := q.GetSubscription(ctx, 2); old.UsedInOctets != 0 || old.UsedSeconds != 0 {
			t.Fatalf("%s: expired subscription got %d bytes %d s", when, old.UsedInOctets, old.UsedSeconds)
		}
	}
	check("migration")
	if _, err := conn.Exec("UPDATE subscriptions SET used_in_octets = 7, used_out_octets = 7, used_seconds = 7"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RecountSubscriptionUsage(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec("UPDATE subscriptions SET used_in_octets = 0, used_seconds = 0 WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	check("recount")
}
