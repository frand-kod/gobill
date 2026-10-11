package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// Bulk "Recharge same plan" from the customer list: confirm (writes nothing), run (sequential,
// per-customer result), role guard and the cap.
func TestBulkRecharge(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	gold := e.plan(t, "gold", "PPPoE", 10000)
	// u1 and u2 are on gold now; off is inactive; nop has no plan
	u2, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "U Two", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	off, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "off", PasswordHash: "h", Fullname: "Off", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Inactive"})
	nop, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "nop", PasswordHash: "h", Fullname: "No Plan", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	for _, c := range []db.Customer{e.cust, u2} {
		if err := e.s.Billing.Recharge(ctx, c.ID, gold.ID, "Cash - seed", 0); err != nil {
			t.Fatal(err)
		}
	}
	trxCount := func(c db.Customer) int {
		rows, _ := e.q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 50})
		return len(rows)
	}
	ids := func(cs ...db.Customer) url.Values {
		v := url.Values{}
		for _, c := range cs {
			v.Add("ids", itoa(c.ID))
		}
		return v
	}

	// the list offers the action to staff
	if b := do(e.h, "GET", "/admin/customers", nil, e.c).Body.String(); !strings.Contains(b, `formaction="/admin/customers/recharge"`) {
		t.Fatal("list lacks the bulk recharge action")
	}

	// confirm: count and total, nothing written; the no-plan and inactive customers are listed as failures
	before := trxCount(e.cust)
	w := do(e.h, "POST", "/admin/customers/recharge", ids(e.cust, u2, off, nop), e.c)
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, "Isi ulang 2 pelanggan, total "+money(20000)) {
		t.Fatalf("confirm: %d\n%s", w.Code, b)
	}
	if !strings.Contains(b, "Tidak ada paket untuk diperpanjang") || !strings.Contains(b, "tidak aktif") {
		t.Errorf("confirm lacks the failure reasons")
	}
	if trxCount(e.cust) != before || len(*e.calls) != 2 { // the two seed recharges called the router once each
		t.Fatalf("confirm wrote data: trx %d->%d calls %v", before, trxCount(e.cust), *e.calls)
	}

	// run with 2 ok and 1 failing (inactive): sequential, summary says ok N, failed M
	w = do(e.h, "POST", "/admin/customers/recharge/run", ids(e.cust, u2, off), e.c)
	b = w.Body.String()
	if w.Code != 200 || !strings.Contains(b, "2 diisi ulang, 1 gagal") {
		t.Fatalf("run summary: %d\n%s", w.Code, b)
	}
	if trxCount(e.cust) != before+1 {
		t.Errorf("u1 transactions %d, want %d", trxCount(e.cust), before+1)
	}
	if n := len(*e.calls); n != 4 { // 2 seed + 2 renewals
		t.Errorf("router calls %d, want 4", n)
	}
	if last, _ := e.q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true}, Limit: 1}); len(last) != 1 || !strings.HasPrefix(last[0].Method, "Cash - ") {
		t.Errorf("renewal is not recorded as Cash: %+v", last)
	}

	// roles: Report is refused; Agent may run it
	rita := login(t, e.h, "rita")
	wantCode(t, do(e.h, "POST", "/admin/customers/recharge", ids(e.cust), rita), 403, "confirm for Report")
	wantCode(t, do(e.h, "POST", "/admin/customers/recharge/run", ids(e.cust), rita), 403, "run for Report")
	mk(t, e.q, "agnes", "Agent", 1)
	wantCode(t, do(e.h, "POST", "/admin/customers/recharge", ids(u2), login(t, e.h, "agnes")), 200, "confirm for Agent")

	// cap and empty selection: sent back to the list with an error, nothing charged
	many := url.Values{}
	for i := 0; i < bulkRechargeMax+1; i++ {
		many.Add("ids", fmt.Sprint(900000+i))
	}
	calls := len(*e.calls)
	for _, v := range []url.Values{many, {}} {
		w := do(e.h, "POST", "/admin/customers/recharge/run", v, e.c)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/customers" {
			t.Errorf("cap/empty: %d %s", w.Code, w.Header().Get("Location"))
		}
	}
	if len(*e.calls) != calls {
		t.Error("cap or empty selection reached the router")
	}
}
