package billing

import (
	"context"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// R3: a Period plan is free only on the very first activation; after expiry the price recorded
// equals the balance charged.
func TestPeriodRepurchasePrice(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 100000, ID: e.cust.ID})
	if _, err := e.conn.Exec("UPDATE plans SET validity_unit = 'Period' WHERE id = ?", e.day.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); err != nil {
		t.Fatal(err)
	}
	if e.balance() != 100000 { // first period: billed 0, so nothing debited
		t.Fatalf("first: balance %d", e.balance())
	}
	if _, err := e.conn.Exec("UPDATE subscriptions SET status = 'expired'"); err != nil {
		t.Fatal(err)
	}
	bal := e.balance()
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); err != nil {
		t.Fatal(err)
	}
	var price int64
	e.conn.QueryRow("SELECT price FROM transactions ORDER BY id DESC LIMIT 1").Scan(&price)
	if price != 10000 || bal-e.balance() != price {
		t.Fatalf("recorded %d charged %d", price, bal-e.balance())
	}
}
