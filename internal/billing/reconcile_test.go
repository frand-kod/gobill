package billing

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
)

// reconEnv has four PPPoE customers: u1 same as the router, u2 other password, u3 other profile,
// u4 missing on the router; the router also holds ro (router only). Passwords are listed in secrets.
func reconEnv(t *testing.T, accts *[]map[string]string, down *error) (*env, []string) {
	ctx, e := context.Background(), setup(t)
	e.s.Key = bytes.Repeat([]byte{3}, 32)
	e.s.RouterFor = func(db.Router) (device.Router, error) {
		return device.Router{Exec: func(_ context.Context, sn []string) ([]map[string]string, error) {
			if *down != nil {
				return nil, *down
			}
			switch sn[0] {
			case "/ppp/secret/print":
				return *accts, nil
			case "/ip/hotspot/user/print":
				return nil, nil
			}
			return []map[string]string{{"name": "Core"}}, nil
		}}, nil
	}
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return upDev{fakeDev: fakeDev{calls: e.calls}}, nil }
	pws := map[string]string{"u1": "PW-ONE", "u2": "PW-TWO", "u3": "PW-THREE", "u4": "PW-FOUR"}
	for _, n := range []string{"u2", "u3", "u4"} {
		enc, _ := secret.Seal(e.s.Key, []byte(pws[n]))
		c, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: n, PasswordHash: "h", Fullname: n, ServiceType: "PPPoE", Status: "Active", SecretEnc: enc})
		if err != nil {
			t.Fatal(err)
		}
		plan := e.day.ID
		if n == "u3" {
			plan = e.day2.ID
		}
		if err := e.s.Recharge(ctx, c.ID, plan, "Admin - Cash", 0); err != nil {
			t.Fatal(err)
		}
	}
	enc, _ := secret.Seal(e.s.Key, []byte(pws["u1"]))
	e.q.SetCustomerSecret(ctx, db.SetCustomerSecretParams{SecretEnc: enc, ID: e.cust.ID})
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	*accts = []map[string]string{
		{"name": "u1", "password": "PW-ONE", "profile": "day"},
		{"name": "u2", "password": "ROUTER-PW", "profile": "day"},
		{"name": "u3", "password": "PW-THREE", "profile": "day"}, // gobill has day2
		{"name": "ro", "password": "SECRET-RO", "profile": "gold"},
	}
	return e, []string{"PW-ONE", "PW-TWO", "PW-THREE", "PW-FOUR", "ROUTER-PW", "SECRET-RO"}
}

func TestReconcileRules(t *testing.T) {
	var accts []map[string]string
	var down error
	e, _ := reconEnv(t, &accts, &down)
	rows, err := e.s.Reconcile(context.Background(), e.day.RouterID.Int64)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Kind + "/" + r.DefaultAction() + "/" + r.Detail
	}
	want := map[string]string{
		"u1": "same/none/",
		"u2": "differs/push/password",
		"u3": "differs/push/profile: router day, gobill day2",
		"u4": "gobill/push/",
		"ro": "router/skip/profile: gold",
	}
	if len(got) != len(want) {
		t.Fatalf("rows: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestReconApplyChoices(t *testing.T) {
	var accts []map[string]string
	var down error
	e, pws := reconEnv(t, &accts, &down)
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)
	ctx := context.Background()
	choice := map[string]string{"u2": ActRouter, "u3": ActPush, "u4": ActSkip, "ro": ActImport}
	*e.calls = nil
	res, err := e.s.ReconApply(ctx, e.day.RouterID.Int64, 0, func(r ReconRow) string {
		if c, ok := choice[r.Name]; ok {
			return c
		}
		return r.DefaultAction()
	}, func(pw string) (string, error) { return "hash", nil })
	if err != nil || len(res.Failures) != 0 || res.Pushed != 1 || res.Followed != 1 || res.Imported != 1 || res.Skipped != 2 {
		t.Fatalf("res %+v err %v", res, err)
	}
	// u3 pushed (plan first, then the account); u4 skipped so not pushed
	if strings.Join(*e.calls, ",") != "plan:day2,add:day2" {
		t.Fatalf("calls %v", *e.calls)
	}
	// u2 follows the router password, sealed; ro is a customer with the router password sealed
	c2, _ := e.q.GetCustomerByUsername(ctx, "u2")
	if pw, err := secret.Open(e.s.Key, c2.SecretEnc); err != nil || string(pw) != "ROUTER-PW" {
		t.Fatalf("u2 secret %q %v", pw, err)
	}
	ro, err := e.q.GetCustomerByUsername(ctx, "ro")
	if err != nil || ro.ServiceType != "PPPoE" {
		t.Fatalf("ro %+v %v", ro, err)
	}
	if pw, err := secret.Open(e.s.Key, ro.SecretEnc); err != nil || string(pw) != "SECRET-RO" || bytes.Contains(ro.SecretEnc, []byte("SECRET-RO")) {
		t.Fatalf("ro secret not sealed")
	}
	for _, p := range pws {
		if strings.Contains(logs.String(), p) {
			t.Fatalf("password %q in log output", p)
		}
		for _, f := range res.Failures {
			if strings.Contains(f, p) {
				t.Fatalf("password %q in failure", p)
			}
		}
	}
	// importing the same name again fails that row only, the customer exists
	res, err = e.s.ReconApply(ctx, e.day.RouterID.Int64, 0, func(r ReconRow) string {
		if r.Kind == ReconRouter {
			return ActImport
		}
		return ActNone
	},
		func(string) (string, error) { return "hash", nil })
	if err != nil || len(res.Failures) != 1 || !strings.Contains(res.Failures[0], "already exists") {
		t.Fatalf("second import: %+v %v", res, err)
	}
}

func TestReconcileRefused(t *testing.T) {
	var accts []map[string]string
	var down error
	e, _ := reconEnv(t, &accts, &down)
	rid := e.day.RouterID.Int64
	// offline: stops with an error, nothing is written
	down = errors.New("dial tcp: i/o timeout")
	n := func() int { var n int; e.conn.QueryRow(`SELECT COUNT(*) FROM customers`).Scan(&n); return n }
	before := n()
	res, err := e.s.ReconApply(context.Background(), rid, 0, func(ReconRow) string { return ActImport }, func(string) (string, error) { return "h", nil })
	if err == nil || !strings.Contains(err.Error(), "router offline") || n() != before || res.Imported+res.Pushed != 0 {
		t.Fatalf("offline: %v %+v", err, res)
	}
	// RADIUS only: refused
	down = nil
	e.conn.Exec(`UPDATE plans SET device = 'Radius'`)
	if _, err := e.s.Reconcile(context.Background(), rid); !errors.Is(err, ErrRadiusRouter) {
		t.Fatalf("radius: %v", err)
	}
}
