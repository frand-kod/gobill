package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

// upDev records plan pushes; failAdd makes every customer push fail.
type upDev struct {
	fakeDev
	failAdd bool
}

func (d upDev) AddCustomer(ctx context.Context, c device.Customer, p device.Plan) error {
	if d.failAdd {
		return errors.New("secret rejected")
	}
	return d.fakeDev.AddCustomer(ctx, c, p)
}
func (d upDev) UpdatePlan(_ context.Context, old string, p device.Plan) error {
	*d.calls = append(*d.calls, "plan:"+p.Name)
	return nil
}

func routerUp(e *env, err *error) {
	e.s.RouterFor = func(db.Router) (device.Router, error) {
		return device.Router{Exec: func(context.Context, []string) ([]map[string]string, error) {
			return []map[string]string{{"name": "Core"}}, *err
		}}, nil
	}
}

func TestSyncRouter(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	var down error
	routerUp(e, &down)
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return upDev{fakeDev: fakeDev{calls: e.calls}}, nil }
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	rid := e.day.RouterID.Int64
	run := func() SyncReport {
		*e.calls = nil
		rep, err := e.s.SyncRouter(ctx, rid, 0)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	// all ok: two plans and one subscription; a second run sends the same, nothing is added
	for i := 0; i < 2; i++ {
		if rep := run(); rep.OK != 3 || rep.Failed != 0 || strings.Join(*e.calls, ",") != "plan:day,plan:day2,add:day" {
			t.Fatalf("run %d: %+v %v", i, rep, *e.calls)
		}
	}
	if n := len(e.subs(t)); n != 1 {
		t.Fatalf("subscriptions: %d", n)
	}

	// one failure keeps its reason, the other items still run
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) {
		return upDev{fakeDev{calls: e.calls}, true}, nil
	}
	if rep := run(); rep.OK != 2 || rep.Failed != 1 || len(rep.Failures) != 1 ||
		rep.Failures[0] != "customer u1: secret rejected" {
		t.Fatalf("failure: %+v", rep)
	}

	// offline: stops before any item
	down = errors.New("dial tcp: i/o timeout")
	*e.calls = nil
	if _, err := e.s.SyncRouter(ctx, rid, 0); err == nil || !strings.Contains(err.Error(), "router offline") || len(*e.calls) != 0 {
		t.Fatalf("offline: %v calls %v", err, *e.calls)
	}
}

func TestSyncRouterRadiusRefused(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	var down error
	routerUp(e, &down)
	e.conn.Exec(`UPDATE plans SET device = 'Radius'`)
	if _, err := e.s.SyncRouter(ctx, e.day.RouterID.Int64, 0); !errors.Is(err, ErrRadiusRouter) {
		t.Fatalf("err = %v", err)
	}
}
