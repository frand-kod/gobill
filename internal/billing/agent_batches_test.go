package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func (e *env) agent(t *testing.T, name string) db.Admin {
	t.Helper()
	a, err := e.q.CreateAdmin(context.Background(), db.CreateAdminParams{Username: name, PasswordHash: "x", Role: "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// batch makes a batch whose codes are PFX1, PFX2, ... and returns it with the ids of its vouchers.
func (e *env) batch(t *testing.T, agent db.Admin, pfx string, paid, bonus, price int64) (int64, []int64) {
	t.Helper()
	n := 0
	id, err := e.s.CreateAgentBatch(context.Background(), BatchParams{AgentID: agent.ID, PlanID: e.day.ID, Paid: paid, Bonus: bonus, Price: price,
		NewCode: func() (string, error) { n++; return fmt.Sprint(pfx, n), nil }})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := 1; i <= int(paid+bonus); i++ {
		v, err := e.q.GetVoucherByCode(context.Background(), fmt.Sprint(pfx, i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	return id, ids
}

func (e *env) debt(t *testing.T, id int64) int64 {
	t.Helper()
	d, err := e.q.AgentBatchDebt(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAgentBatchDebtBonusAndMarkup(t *testing.T) {
	e := setup(t)
	ag := e.agent(t, "ag")
	// 10 paid + 4 bonus at 5000: 14 codes, debt for the 10 paid only
	id, ids := e.batch(t, ag, "BON", 10, 4, 5000)
	if len(ids) != 14 || e.debt(t, id) != 50000 {
		t.Fatalf("codes %d debt %d", len(ids), e.debt(t, id))
	}
	var n int
	e.conn.QueryRow(`SELECT COUNT(*) FROM agent_batch_vouchers WHERE batch_id = ?`, id).Scan(&n)
	if n != 14 {
		t.Fatalf("linked %d", n)
	}
	// markup model: bonus 0, price = agent price
	id2, ids2 := e.batch(t, ag, "MUP", 20, 0, 5000)
	if len(ids2) != 20 || e.debt(t, id2) != 100000 {
		t.Fatalf("markup debt %d", e.debt(t, id2))
	}
	// the vouchers redeem like any other and the usage shows in the list
	if err := e.s.RedeemVoucher(context.Background(), "BON1", e.cust.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := e.q.ListAgentBatches(context.Background(), db.ListAgentBatchesParams{AgentID: ag.ID, PageLimit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	if r := rows[1]; r.ID != id || r.Used != 1 || r.Returned != 0 || r.Debt != 50000 {
		t.Fatalf("row %+v", r)
	}
}

func TestAgentBatchPayment(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	id, _ := e.batch(t, e.agent(t, "ag"), "P", 10, 4, 5000)
	if err := e.s.RecordAgentPayment(ctx, id, 20000, "cash", 0); err != nil || e.debt(t, id) != 30000 {
		t.Fatalf("pay: %v debt %d", err, e.debt(t, id))
	}
	for _, a := range []int64{0, -5} {
		if err := e.s.RecordAgentPayment(ctx, id, a, "", 0); !errors.Is(err, ErrBatchInvalid) {
			t.Fatalf("amount %d: %v", a, err)
		}
	}
	if err := e.s.RecordAgentPayment(ctx, id, 30001, "", 0); !errors.Is(err, ErrOverpay) || e.debt(t, id) != 30000 {
		t.Fatalf("overpay: %v debt %d", err, e.debt(t, id))
	}
	if err := e.s.RecordAgentPayment(ctx, id, 30000, "", 0); err != nil || e.debt(t, id) != 0 {
		t.Fatalf("settle: %v", err)
	}
	if err := e.s.RecordAgentPayment(ctx, id, 1, "", 0); !errors.Is(err, ErrOverpay) {
		t.Fatalf("pay on zero debt: %v", err)
	}
	if err := e.s.RecordAgentPayment(ctx, 999, 1, "", 0); !errors.Is(err, ErrBatchNotFound) {
		t.Fatalf("unknown batch: %v", err)
	}
}

func TestAgentBatchReturnCap(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	id, ids := e.batch(t, e.agent(t, "ag"), "R", 10, 4, 5000)
	// used codes cannot be returned
	if err := e.s.RedeemVoucher(ctx, "R14", e.cust.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.ReturnAgentVouchers(ctx, id, ids[13:], 0); !errors.Is(err, ErrNotReturnable) {
		t.Fatalf("used code: %v", err)
	}
	// 4 returns, each lowers the debt by 5000
	if err := e.s.ReturnAgentVouchers(ctx, id, ids[10:13], 0); err != nil || e.debt(t, id) != 35000 {
		t.Fatalf("return 3: %v debt %d", err, e.debt(t, id))
	}
	// the same code twice, in one call or across calls
	if err := e.s.ReturnAgentVouchers(ctx, id, []int64{ids[0], ids[0]}, 0); !errors.Is(err, ErrNotReturnable) {
		t.Fatalf("dup in call: %v", err)
	}
	if err := e.s.ReturnAgentVouchers(ctx, id, ids[10:11], 0); !errors.Is(err, ErrNotReturnable) {
		t.Fatalf("again: %v", err)
	}
	// 3 done, cap is the paid count (10): 8 more is refused as a whole, 7 passes
	if err := e.s.ReturnAgentVouchers(ctx, id, ids[0:8], 0); !errors.Is(err, ErrReturnLimit) || e.debt(t, id) != 35000 {
		t.Fatalf("over cap: %v debt %d", err, e.debt(t, id))
	}
	if err := e.s.ReturnAgentVouchers(ctx, id, ids[0:7], 0); err != nil || e.debt(t, id) != 0 {
		t.Fatalf("to cap: %v debt %d", err, e.debt(t, id))
	}
	if err := e.s.ReturnAgentVouchers(ctx, id, ids[7:8], 0); !errors.Is(err, ErrReturnLimit) {
		t.Fatalf("past cap: %v", err)
	}
	// debt can never go below zero, even around the app: the ledger refuses the row
	_, err := e.conn.Exec(`INSERT INTO agent_ledger (batch_id, kind, amount) VALUES (?, 'payment', 1)`, id)
	if err == nil || !strings.Contains(err.Error(), "credit exceeds debt") {
		t.Fatalf("raw overpay: %v", err)
	}
	_, err = e.conn.Exec(`INSERT INTO agent_ledger (batch_id, kind, amount, voucher_id) VALUES (?, 'return', 0, ?)`, id, ids[8])
	if err == nil {
		t.Fatal("raw return past the cap accepted")
	}
}

func TestAgentBatchReturnForeignAndPayAfterReturn(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	ag := e.agent(t, "ag")
	a, ia := e.batch(t, ag, "A", 2, 0, 1000)
	b, ib := e.batch(t, ag, "B", 2, 0, 1000)
	if err := e.s.ReturnAgentVouchers(ctx, a, ib[:1], 0); !errors.Is(err, ErrNotReturnable) {
		t.Fatalf("code of another batch: %v", err)
	}
	// a plain voucher outside any batch
	v, _ := e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: "PLAIN", PlanID: e.day.ID})
	if err := e.s.ReturnAgentVouchers(ctx, a, []int64{v.ID}, 0); !errors.Is(err, ErrNotReturnable) {
		t.Fatalf("plain voucher: %v", err)
	}
	if err := e.s.ReturnAgentVouchers(ctx, a, ia[:1], 0); err != nil {
		t.Fatal(err)
	}
	// a payment can only cover what is left after the return
	if err := e.s.RecordAgentPayment(ctx, a, 1001, "", 0); !errors.Is(err, ErrOverpay) {
		t.Fatalf("pay over: %v", err)
	}
	if e.debt(t, a) != 1000 || e.debt(t, b) != 2000 {
		t.Fatalf("debts %d %d", e.debt(t, a), e.debt(t, b))
	}
}

// A cancelled code no longer redeems (portal and admin path), and a used one is not touched.
func TestAgentReturnedCodeCannotRedeem(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	id, ids := e.batch(t, e.agent(t, "ag"), "X", 2, 0, 1000)
	if err := e.s.ReturnAgentVouchers(ctx, id, ids[:1], 0); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RedeemVoucher(ctx, "X1", e.cust.ID); !errors.Is(err, ErrVoucherInvalid) {
		t.Fatalf("returned code redeemed: %v", err)
	}
	if n, err := e.q.UseVoucher(ctx, db.UseVoucherParams{UsedBy: nullID(e.cust.ID), ID: ids[0]}); err != nil || n != 0 {
		t.Fatalf("UseVoucher on a returned code claimed %d rows (%v)", n, err)
	}
	if len(e.subs(t)) != 0 {
		t.Fatal("subscription created for a cancelled code")
	}
	if err := e.s.RedeemVoucher(ctx, "X2", e.cust.ID); err != nil {
		t.Fatalf("other code: %v", err)
	}
}

func TestAgentBatchRules(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	ag := e.agent(t, "ag")
	adm, _ := e.q.CreateAdmin(ctx, db.CreateAdminParams{Username: "adm", PasswordHash: "x", Role: "Admin"})
	gen := func() (string, error) { return "Z", nil }
	for name, p := range map[string]BatchParams{
		"not an agent": {AgentID: adm.ID, PlanID: e.day.ID, Paid: 1, Price: 1, NewCode: gen},
		"no codes":     {AgentID: ag.ID, PlanID: e.day.ID, NewCode: gen},
		"negative":     {AgentID: ag.ID, PlanID: e.day.ID, Paid: 2, Bonus: -1, Price: 1, NewCode: gen},
		"neg price":    {AgentID: ag.ID, PlanID: e.day.ID, Paid: 1, Price: -1, NewCode: gen},
		"no plan":      {AgentID: ag.ID, PlanID: 999, Paid: 1, Price: 1, NewCode: gen},
		"too many":     {AgentID: ag.ID, PlanID: e.day.ID, Paid: 501, Price: 1, NewCode: gen},
	} {
		if _, err := e.s.CreateAgentBatch(ctx, p); !errors.Is(err, ErrBatchInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// bonus only (no paid) is a gift batch with no debt
	id, err := e.s.CreateAgentBatch(ctx, BatchParams{AgentID: ag.ID, PlanID: e.day.ID, Bonus: 1, Price: 5000,
		NewCode: func() (string, error) { return "GIFT", nil }})
	if err != nil || e.debt(t, id) != 0 {
		t.Fatalf("gift batch: %v", err)
	}
}

// A failure while the codes are made rolls back the batch, its vouchers and the debt.
func TestAgentBatchRollback(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	ag := e.agent(t, "ag")
	n := 0
	_, err := e.s.CreateAgentBatch(ctx, BatchParams{AgentID: ag.ID, PlanID: e.day.ID, Paid: 5, Bonus: 2, Price: 1000,
		NewCode: func() (string, error) {
			if n++; n == 6 {
				return "", errors.New("entropy gone")
			}
			return fmt.Sprint("RB", n), nil
		}})
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, tbl := range []string{"agent_batches", "agent_batch_vouchers", "agent_ledger", "vouchers"} {
		var c int
		e.conn.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&c)
		if c != 0 {
			t.Errorf("%s has %d rows after rollback", tbl, c)
		}
	}
	// a clash with an existing code is redrawn; endless clashes fail whole
	e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: "TAKEN", PlanID: e.day.ID})
	calls := 0
	if _, err := e.s.CreateAgentBatch(ctx, BatchParams{AgentID: ag.ID, PlanID: e.day.ID, Paid: 1, Price: 1,
		NewCode: func() (string, error) {
			if calls++; calls == 1 {
				return "TAKEN", nil
			}
			return "FREE", nil
		}}); err != nil {
		t.Fatalf("redraw: %v", err)
	}
	if _, err := e.s.CreateAgentBatch(ctx, BatchParams{AgentID: ag.ID, PlanID: e.day.ID, Paid: 1, Price: 1,
		NewCode: func() (string, error) { return "TAKEN", nil }}); err == nil {
		t.Fatal("endless clash must fail")
	}
}

// A failed return or payment leaves the ledger as it was (the whole call is one transaction).
func TestAgentReturnRollsBackWholeCall(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	id, ids := e.batch(t, e.agent(t, "ag"), "RR", 3, 0, 1000)
	// the second code is used, so the first, valid return must not stick
	if err := e.s.RedeemVoucher(ctx, "RR2", e.cust.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.ReturnAgentVouchers(ctx, id, []int64{ids[0], ids[1]}, 0); !errors.Is(err, ErrNotReturnable) {
		t.Fatal(err)
	}
	if e.debt(t, id) != 3000 {
		t.Fatalf("debt %d after failed return", e.debt(t, id))
	}
	if err := e.s.RedeemVoucher(ctx, "RR1", e.cust.ID); err != nil {
		t.Fatalf("code of a failed return must still redeem: %v", err)
	}
}

// The cap on returns holds in the schema too, also when the price is 0 and the credit check cannot catch it.
func TestAgentReturnCapZeroPrice(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	id, ids := e.batch(t, e.agent(t, "ag"), "Q", 1, 1, 0)
	if err := e.s.ReturnAgentVouchers(ctx, id, ids[:1], 0); err != nil {
		t.Fatal(err)
	}
	_, err := e.conn.Exec(`INSERT INTO agent_ledger (batch_id, kind, amount, voucher_id) VALUES (?, 'return', 0, ?)`, id, ids[1])
	if err == nil || !strings.Contains(err.Error(), "returns exceed") {
		t.Fatalf("raw return past the cap: %v", err)
	}
}

func TestAgentLedgerAppendOnly(t *testing.T) {
	e := setup(t)
	id, _ := e.batch(t, e.agent(t, "ag"), "L", 1, 0, 1000)
	for _, q := range []string{`UPDATE agent_ledger SET amount = 0`, `DELETE FROM agent_ledger`} {
		if _, err := e.conn.Exec(q); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v", q, err)
		}
	}
	if e.debt(t, id) != 1000 {
		t.Fatal("debt changed")
	}
}
