package billing

import (
	"context"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
)

// start_on_first_login: a RADIUS-plan recharge waits for the first login, the expiry job leaves it alone.
func TestStartOnFirstLogin(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	key := make([]byte, 32)
	enc, _ := secret.Seal(key, []byte("pw"))
	if _, err := e.conn.Exec("UPDATE customers SET secret_enc = ?; UPDATE plans SET router_id = NULL, device = 'Radius'", enc); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "start_on_first_login", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t); sub.PendingStart != 1 {
		t.Fatalf("radius plan recharge not pending: %+v", sub)
	}
	e.now = e.now.AddDate(0, 0, 2) // past the provisional expiry, customer not logged in yet
	if err := e.s.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t); sub.Status != "active" || sub.PendingStart != 1 {
		t.Fatalf("expiry job touched a pending row: %+v", sub)
	}

	srv := &radius.Server{Q: e.q, Key: key, Now: func() time.Time { return e.now }, Start: e.s.StartPending}
	check := func(pw []byte) (bool, string) { return string(pw) == "pw", "" }
	if d := srv.Authorize(ctx, radius.AuthRequest{User: "u1", Check: check}); d.Reject != "" {
		t.Fatalf("first login rejected: %s", d.Reject)
	}
	sub := e.sub(t)
	if want := e.now.AddDate(0, 0, 1).Unix(); sub.PendingStart != 0 || sub.StartedAt != e.now.Unix() || sub.ExpiresAt != want {
		t.Fatalf("first login: %+v, want expiry %d", sub, want)
	}
	e.now = e.now.Add(time.Hour)
	if d := srv.Authorize(ctx, radius.AuthRequest{User: "u1", Check: check}); d.Reject != "" {
		t.Fatalf("second login rejected: %s", d.Reject)
	}
	if again := e.sub(t); again.ExpiresAt != sub.ExpiresAt || again.StartedAt != sub.StartedAt {
		t.Fatalf("second login moved the period: %+v", again)
	}
}

// Setting off, or a MikroTik plan (router set), keeps the old behaviour: the period starts at recharge.
func TestStartOnFirstLoginOffOrMikrotik(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if _, err := e.conn.Exec("UPDATE plans SET router_id = NULL, device = 'Radius' WHERE id = ?", e.day.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if sub := e.sub(t); sub.PendingStart != 0 || sub.ExpiresAt != e.now.AddDate(0, 0, 1).Unix() {
		t.Fatalf("setting off: %+v", sub)
	}

	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "start_on_first_login", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.conn.Exec("UPDATE subscriptions SET status = 'expired'"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Recharge(ctx, e.cust.ID, e.day2.ID, "Admin - Cash", 0); err != nil { // day2 has a router: MikroTik plan
		t.Fatal(err)
	}
	if sub := e.sub(t); sub.PendingStart != 0 {
		t.Fatalf("MikroTik plan pending: %+v", sub)
	}
}
