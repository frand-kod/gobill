package billing

import (
	"context"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func claimOK(*db.Queries) (bool, error) { return true, nil }

// Audit #3: a first-time Period plan paid through a gateway must record what the gateway charged.
func TestAuditMoney3_FirstPeriodGatewayPrice(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.conn.Exec("UPDATE plans SET validity_unit = 'Period' WHERE id = ?", e.day.ID)
	if err := e.s.RechargePaid(ctx, claimOK, e.cust.ID, e.day.ID, "Tripay - QRIS", "", 10000); err != nil {
		t.Fatal(err)
	}
	if p, _ := lastPrice(e); p != 10000 {
		t.Fatalf("gateway charged 10000, transaction records %d", p)
	}
}

// Audit #4: a coupon with max_usage 1 must not discount two gateway orders.
func TestAuditMoney4_CouponMaxUsageGateway(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.conn.Exec(`INSERT INTO coupons (code, type, value, max_usage, start_date, end_date) VALUES ('ONE','fixed',1000,1,'2000-01-01','2999-12-31')`)
	// both orders were priced while used = 0 (Discount passes for each); both then settle
	for i := 0; i < 2; i++ {
		if err := e.s.RechargePaid(ctx, claimOK, e.cust.ID, e.day.ID, "Tripay - QRIS", "ONE", 9000); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	e.conn.QueryRow(`SELECT COUNT(*) FROM transactions WHERE note LIKE '%Coupon ONE%'`).Scan(&n)
	if n > 1 {
		t.Fatalf("coupon with max_usage 1 discounted %d paid orders", n)
	}
}

// Audit #5: the report total must agree with the dashboard income (a balance-funded purchase is not new income).
func TestAuditMoney5_ReportDoubleCountsBalance(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	if err := e.s.Deposit(ctx, e.cust.ID, 0, 10000, "", 0); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0); err != nil {
		t.Fatal(err)
	}
	rep, err := e.q.ReportTotals(ctx, db.ReportTotalsParams{FromTs: 0, ToTs: 1 << 40})
	if err != nil {
		t.Fatal(err)
	}
	dash, err := e.q.SumTransactionsBetween(ctx, db.SumTransactionsBetweenParams{CreatedAt: 0, CreatedAt_2: 1 << 40})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != dash {
		t.Fatalf("report income %d, dashboard income %d", rep.Total, dash)
	}
}

// Audit #6 (guard, passes: trigger in migrations/0022_agent_batches.sql:49): returning a voucher after the batch is fully paid must not leave a negative debt.
func TestAuditMoney6_ReturnAfterFullPayment(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	id, ids := e.batch(t, e.agent(t, "ag"), "RET", 2, 0, 1000)
	if err := e.s.RecordAgentPayment(ctx, id, 2000, "", 0); err != nil {
		t.Fatal(err)
	}
	err := e.s.ReturnAgentVouchers(ctx, id, ids[:1], 0)
	if d := e.debt(t, id); err == nil && d < 0 {
		t.Fatalf("debt %d after return on a paid batch", d)
	}
}

// Audit #8: a paid gateway order whose plan was deleted must not lose the money.
func TestAuditMoney8_PaidOrderPlanGone(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	err := e.s.RechargePaid(ctx, claimOK, e.cust.ID, 99999, "Tripay - QRIS", "", 10000)
	if err != nil || e.balance() != 10000 {
		t.Fatalf("paid 10000 for a missing plan: err=%v balance=%d (claim rolled back, nothing credited, no alert)", err, e.balance())
	}
}

// Audit #9 (guard, passes): a negative custom-field bill must never raise the balance.
func TestAuditMoney9_NegativeBillGuard(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	e.s.Deposit(ctx, e.cust.ID, 0, 5000, "", 0)
	setAttr(ctx, e.q, e.cust.ID, "Router Bill", "-50000")
	_ = e.s.RechargeWithBalance(ctx, e.cust.ID, e.day.ID, 0)
	if b := e.balance(); b > 5000 {
		t.Fatalf("balance rose to %d", b)
	}
}
