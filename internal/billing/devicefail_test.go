package billing

import (
	"context"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

// A failed router call is reported through TrackDeviceFailure; the recharge itself is unchanged.
func TestTrackDeviceFailure(t *testing.T) {
	e := setup(t)
	ctx, failed := TrackDeviceFailure(context.Background())
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil || *failed {
		t.Fatalf("working router: err=%v failed=%v", err, *failed)
	}
	e.s.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return failDev{fakeDev{calls: e.calls}}, nil }
	ctx, failed = TrackDeviceFailure(context.Background())
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil || !*failed {
		t.Fatalf("broken router: err=%v failed=%v", err, *failed)
	}
	if trx, _ := e.q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: nullID(e.cust.ID), Limit: 10}); len(trx) != 2 {
		t.Fatalf("transactions: %d", len(trx))
	}
}
