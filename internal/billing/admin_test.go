package billing

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestDeposit(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	// nil notifier: must not panic
	if err := e.s.Deposit(ctx, e.cust.ID, 0, 7000, "cash in", 0); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Deposit(ctx, e.cust.ID, e.topup.ID, 0, "", 0); err != nil {
		t.Fatal(err)
	}
	if e.balance() != 57000 {
		t.Fatalf("balance %d", e.balance())
	}
	trx, _ := e.q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true}, Limit: 10})
	if len(trx) != 2 || trx[1].Note != "cash in" || trx[1].Type != "Balance" || trx[1].Price != 7000 {
		t.Fatalf("trx %+v", trx)
	}
	if err := e.s.Deposit(ctx, e.cust.ID, e.day.ID, 0, "", 0); !errors.Is(err, ErrBadDeposit) {
		t.Fatalf("non-balance plan: %v", err)
	}
	if err := e.s.Deposit(ctx, e.cust.ID, 0, 0, "", 0); !errors.Is(err, ErrBadDeposit) {
		t.Fatalf("zero amount: %v", err)
	}
	if e.balance() != 57000 {
		t.Fatal("rejected deposits changed the balance")
	}

	got := withNotify(t, e)
	if err := e.s.Deposit(ctx, e.cust.ID, e.topup.ID, 0, "", 0); err != nil {
		t.Fatal(err)
	}
	if reqs := drain(got); count(reqs, "payment.paid") != 1 {
		t.Fatalf("deposit notification: %v", reqs)
	}
}

func TestEditExtendDeactivate(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	sub := e.sub(t)

	want := e.now.AddDate(0, 0, 10)
	if err := e.s.EditSubscription(ctx, sub.ID, e.day.ID, want, 0); err != nil {
		t.Fatal(err)
	}
	if got := e.sub(t); got.ExpiresAt != want.Unix() || got.PlanID != e.day.ID {
		t.Fatalf("edit: %+v", got)
	}
	// plan change removes the old profile, adds the new one
	*e.calls = nil
	if err := e.s.EditSubscription(ctx, sub.ID, e.day2.ID, want, 0); err != nil {
		t.Fatal(err)
	}
	if got := e.sub(t); got.PlanID != e.day2.ID || len(*e.calls) < 2 || (*e.calls)[0] != "remove:day" || (*e.calls)[1] != "add:day2" {
		t.Fatalf("plan change: %+v calls %v", got, *e.calls)
	}

	if err := e.s.ExtendSubscription(ctx, sub.ID, 3, 0); err != nil {
		t.Fatal(err)
	}
	if got := e.sub(t); got.ExpiresAt != want.AddDate(0, 0, 3).Unix() {
		t.Fatalf("extend: %v", time.Unix(got.ExpiresAt, 0))
	}

	*e.calls = nil
	for range 2 {
		if err := e.s.DeactivateSubscription(ctx, sub.ID, 0); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.q.GetSubscription(ctx, sub.ID)
	if got.Status != "expired" || got.ExpiresAt != e.now.Unix() || len(*e.calls) != 1 || (*e.calls)[0] != "remove:day2" {
		t.Fatalf("deactivate: %+v calls %v", got, *e.calls)
	}
	logs, _ := e.q.ListActivityLogs(ctx, db.ListActivityLogsParams{Limit: 50})
	if len(logs) < 4 {
		t.Fatalf("activity logs: %d", len(logs))
	}
}
