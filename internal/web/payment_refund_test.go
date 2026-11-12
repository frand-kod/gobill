package web

import (
	"database/sql"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

// money#7: a gateway REFUND on an already-paid request is logged for the operator and never reverses the recharge.
func TestSettleRefundOnPaidIsLoggedNotReversed(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	pr, err := e.q.CreatePaymentRequest(t.Context(), db.CreatePaymentRequestParams{Ref: "R1", Gateway: "tripay", CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true},
		Username: "u1", PlanID: p.ID, Amount: 10000, ExpiresAt: 1 << 40})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.SettlePayment(t.Context(), pr, payment.Paid); err != nil {
		t.Fatal(err)
	}
	pr, _ = e.q.GetPaymentRequestByRef(t.Context(), "R1")
	if err := e.s.SettlePayment(t.Context(), pr, payment.Refunded); err != nil {
		t.Fatal(err)
	}
	var n, trx int
	e.s.conn.QueryRow(`SELECT COUNT(*) FROM activity_logs WHERE action = 'payment_refunded'`).Scan(&n)
	e.s.conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&trx)
	if got, _ := e.q.GetPaymentRequestByRef(t.Context(), "R1"); n != 1 || trx != 1 || got.Status != "paid" {
		t.Fatalf("logs %d, transactions %d, status %s", n, trx, got.Status)
	}
}
