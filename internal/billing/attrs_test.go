package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestCustomerBills(t *testing.T) {
	items, total := customerBills(map[string]string{
		"Router Bill": "5000", "Phone Bill": "3000:2", "Done Bill": "9000:0", "Invoice": "123", "Other": "7",
	})
	if total != 8000 || len(items) != 2 || items[0].Name != "Phone Bill" || !items[0].Inst {
		t.Fatalf("items %+v total %d", items, total)
	}
}

// Bills add to the recharge price, an installment loses one remaining per paid recharge,
// and a balance payment debits the total.
func TestRechargeAddsBills(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := setAttr(ctx, e.q, e.cust.ID, "Router Bill", "500"); err != nil {
		t.Fatal(err)
	}
	if err := setAttr(ctx, e.q, e.cust.ID, "Phone Bill", "300:1"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	trx, err := e.q.GetTransactionByInvoice(ctx, "INV-2501-000001")
	if err != nil || trx.Price != 10800 {
		t.Fatalf("trx %+v %v", trx, err)
	}
	if got := attrs(ctx, e.q, e.cust.ID)["Phone Bill"]; got != "300:0" {
		t.Fatalf("installment = %q", got)
	}
	// installment finished: only the fixed bill is left
	if err := e.s.Recharge(ctx, e.cust.ID, e.day.ID, "Admin - Cash", 0); err != nil {
		t.Fatal(err)
	}
	if trx, _ = e.q.GetTransactionByInvoice(ctx, "INV-2501-000002"); trx.Price != 10500 {
		t.Fatalf("second price %d", trx.Price)
	}
	// balance: 10500 needed
	if _, err := e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 10400, ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("err %v", err)
	}
	if _, err := e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 100, ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); err != nil || e.balance() != 0 {
		t.Fatalf("err %v balance %d", err, e.balance())
	}
}
