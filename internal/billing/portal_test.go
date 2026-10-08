package billing

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func set(t *testing.T, e *env, kv ...string) {
	t.Helper()
	for i := 0; i < len(kv); i += 2 {
		if err := e.q.UpsertSetting(context.Background(), db.UpsertSettingParams{Key: kv[i], Value: kv[i+1]}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTransferBalance(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	to, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Two", ServiceType: "PPPoE", Status: "Active"})
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 10000, ID: e.cust.ID})
	if err := e.s.TransferBalance(ctx, e.cust.ID, "u2", 1000); !errors.Is(err, ErrTransferDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	set(t, e, "allow_balance_transfer", "yes", "minimum_transfer", "500")
	for _, c := range []struct {
		user string
		amt  int64
		want error
	}{{"u1", 1000, ErrSelfTransfer}, {"nobody", 1000, ErrTargetNotFound}, {"u2", 100, ErrBelowMinimum}, {"u2", 20000, ErrInsufficientBalance}} {
		if err := e.s.TransferBalance(ctx, e.cust.ID, c.user, c.amt); !errors.Is(err, c.want) {
			t.Fatalf("%s %d: got %v want %v", c.user, c.amt, err, c.want)
		}
	}
	if err := e.s.TransferBalance(ctx, e.cust.ID, "u2", 3000); err != nil {
		t.Fatal(err)
	}
	got, _ := e.q.GetCustomer(ctx, to.ID)
	if e.balance() != 7000 || got.Balance != 3000 {
		t.Fatalf("balances %d / %d", e.balance(), got.Balance)
	}
	for _, id := range []int64{e.cust.ID, to.ID} {
		if trx := e.trxCount(t, id); trx != 1 {
			t.Fatalf("customer %d trx rows %d", id, trx)
		}
	}
}

func (e *env) trxCount(t *testing.T, id int64) int {
	var n int
	if err := e.conn.QueryRow(`SELECT count(*) FROM transactions WHERE customer_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A failure while crediting the receiver must roll the debit and the sender's row back.
func TestTransferAtomic(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	to, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Two", ServiceType: "PPPoE", Status: "Active"})
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 10000, ID: e.cust.ID})
	set(t, e, "allow_balance_transfer", "yes")
	if _, err := e.conn.Exec(`CREATE TRIGGER fail_credit BEFORE UPDATE OF balance ON customers WHEN NEW.id = ` + strconv.FormatInt(to.ID, 10) +
		` BEGIN SELECT RAISE(ABORT, 'credit failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := e.s.TransferBalance(ctx, e.cust.ID, "u2", 4000); err == nil {
		t.Fatal("expected failure")
	}
	if e.balance() != 10000 || e.trxCount(t, e.cust.ID) != 0 || e.trxCount(t, to.ID) != 0 {
		t.Fatalf("not rolled back: balance %d", e.balance())
	}
}

func TestExtendExpired(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	sub := e.sub(t)
	if _, err := e.s.ExtendExpired(ctx, e.cust.ID, sub.ID); !errors.Is(err, ErrExtendDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	set(t, e, "extend_expired", "1", "extend_days", "3")
	if _, err := e.s.ExtendExpired(ctx, e.cust.ID, sub.ID); !errors.Is(err, ErrNotExpired) {
		t.Fatalf("active sub: %v", err)
	}
	e.q.ExpireSubscription(ctx, sub.ID)
	other, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Two", ServiceType: "PPPoE", Status: "Active"})
	if _, err := e.s.ExtendExpired(ctx, other.ID, sub.ID); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("foreign sub: %v", err)
	}
	until, err := e.s.ExtendExpired(ctx, e.cust.ID, sub.ID)
	if err != nil || !until.Equal(e.now.AddDate(0, 0, 3)) {
		t.Fatalf("extend: %v %v", until, err)
	}
	if got := e.sub(t); got.ID != sub.ID || got.ExpiresAt != until.Unix() {
		t.Fatalf("sub %+v", got)
	}
	// expired again in the same month: refused; next month: allowed
	e.q.ExpireSubscription(ctx, sub.ID)
	if _, err := e.s.ExtendExpired(ctx, e.cust.ID, sub.ID); !errors.Is(err, ErrExtendAlready) {
		t.Fatalf("second extend: %v", err)
	}
	e.now = e.now.AddDate(0, 1, 0)
	if _, err := e.s.ExtendExpired(ctx, e.cust.ID, sub.ID); err != nil {
		t.Fatalf("next month: %v", err)
	}
}
