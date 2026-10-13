package web

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// R2: methods come from payment_usings; the record is "<Method> - <admin name>" like PHP;
// Zero activates the plan at price 0; unknown methods are refused.
func TestRechargeMethods(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "payment_usings", Value: "transfer bank, QRIS,,Cash"}); err != nil {
		t.Fatal(err)
	}
	base := "/admin/customers/" + itoa(e.cust.ID)
	if body := do(e.h, "GET", base, nil, e.c).Body.String(); !strings.Contains(body, `value="transfer bank"`) || !strings.Contains(body, `value="Zero"`) {
		t.Fatal("form lacks payment_usings / Zero options")
	}
	last := func() db.Transaction {
		trx, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true}, Limit: 10})
		return trx[0]
	}
	wantCode(t, do(e.h, "POST", base+"/recharge", url.Values{"plan": {itoa(p.ID)}, "method": {"transfer bank"}}, e.c), 303, "transfer")
	if x := last(); x.Method != "Transfer Bank - Alice Wong" || x.Price != 10000 {
		t.Fatalf("%+v", x)
	}
	wantCode(t, do(e.h, "POST", base+"/recharge", url.Values{"plan": {itoa(p.ID)}, "method": {"Zero"}}, e.c), 303, "zero")
	if x := last(); x.Method != "Recharge Zero - Alice Wong" || x.Price != 0 {
		t.Fatalf("%+v", x)
	}
	wantCode(t, do(e.h, "POST", base+"/recharge", url.Values{"plan": {itoa(p.ID)}, "method": {"Bitcoin"}}, e.c), 303, "unknown")
	if last().Method != "Recharge Zero - Alice Wong" {
		t.Fatal("unknown method recharged")
	}
}

// With enable_balance=no the recharge form does not offer Balance, and a posted Balance is refused without a debit.
func TestRechargeBalanceDisabled(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	ctx := t.Context()
	if _, err := e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 50000, ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "enable_balance", Value: "no"}); err != nil {
		t.Fatal(err)
	}
	if body := do(e.h, "GET", "/admin/recharge?customer=u1", nil, e.c).Body.String(); strings.Contains(body, `value="Balance"`) {
		t.Fatal("Balance offered while enable_balance=no")
	}
	do(e.h, "POST", "/admin/customers/"+itoa(e.cust.ID)+"/recharge", url.Values{"plan": {itoa(p.ID)}, "method": {"Balance"}}, e.c)
	c, _ := e.q.GetCustomer(ctx, e.cust.ID)
	if c.Balance != 50000 || len(*e.calls) != 0 {
		t.Fatalf("balance %d calls %v: Balance was charged while disabled", c.Balance, *e.calls)
	}
}
