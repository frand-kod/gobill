package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

var bulkTokenRe = regexp.MustCompile(`name="token" value="([0-9a-f]+)"`)

// Bulk "Recharge same plan": confirm stores a one-time token (writes nothing); the run charges only
// the stored customers, once; the result page reloads without charging.
func TestBulkRecharge(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	gold := e.plan(t, "gold", "PPPoE", 10000)
	u2, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "U Two", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	off, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "off", PasswordHash: "h", Fullname: "Off", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Inactive"})
	nop, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "nop", PasswordHash: "h", Fullname: "No Plan", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	u3, _ := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u3", PasswordHash: "h", Fullname: "U Three", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	for _, c := range []db.Customer{e.cust, u2, u3} { // all three are on gold; u3 is never selected
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
	token := func(body string) string {
		m := bulkTokenRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no token in page: %s", body)
		}
		return m[1]
	}
	runForm := func(tok string, extra ...url.Values) url.Values {
		v := url.Values{"token": {tok}}
		for _, x := range extra {
			for k, vs := range x {
				v[k] = append(v[k], vs...)
			}
		}
		return v
	}

	// the list offers the action to staff
	if b := do(e.h, "GET", "/admin/customers", nil, e.c).Body.String(); !strings.Contains(b, `formaction="/admin/customers/recharge"`) {
		t.Fatal("list lacks the bulk recharge action")
	}

	// confirm: count and total, a token, nothing written; the no-plan and inactive customers are failures
	calls := len(*e.calls)
	before := trxCount(e.cust)
	w := do(e.h, "POST", "/admin/customers/recharge", ids(e.cust, u2, off, nop), e.c)
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, "Isi ulang 2 pelanggan, total "+money(20000)) {
		t.Fatalf("confirm: %d\n%s", w.Code, b)
	}
	if !strings.Contains(b, "Tidak ada paket untuk diperpanjang") || !strings.Contains(b, "tidak aktif") {
		t.Errorf("confirm lacks the failure reasons")
	}
	tok := token(b)
	if trxCount(e.cust) != before || len(*e.calls) != calls {
		t.Fatalf("confirm wrote data: trx %d->%d calls %d->%d", before, trxCount(e.cust), calls, len(*e.calls))
	}

	// tampered ids are ignored: the run posts u3 and off, but only the stored u1 and u2 are charged
	w = do(e.h, "POST", "/admin/customers/recharge/run", runForm(tok, ids(u3, off)), e.c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != bulkResultPath {
		t.Fatalf("run: %d %s", w.Code, w.Header().Get("Location"))
	}
	if trxCount(e.cust) != before+1 || trxCount(u2) != 2 || trxCount(u3) != 1 || trxCount(off) != 0 {
		t.Errorf("tampered ids were charged: u1 %d u2 %d u3 %d off %d", trxCount(e.cust), trxCount(u2), trxCount(u3), trxCount(off))
	}
	if n := len(*e.calls); n != calls+2 { // 2 renewals
		t.Fatalf("router calls %d, want %d", n, calls+2)
	}

	// the result page shows the summary; a reload shows it again and charges nothing
	after := len(*e.calls)
	for i := 0; i < 2; i++ {
		w = do(e.h, "GET", bulkResultPath, nil, e.c)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "2 diisi ulang, 0 gagal") {
			t.Fatalf("result %d: %d\n%s", i, w.Code, w.Body.String())
		}
	}
	if len(*e.calls) != after || trxCount(e.cust) != before+1 {
		t.Errorf("reloading the result recharged: calls %d->%d", after, len(*e.calls))
	}

	// a second submit of the same token recharges nothing
	w = do(e.h, "POST", "/admin/customers/recharge/run", runForm(tok), e.c)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Sudah diproses") {
		t.Errorf("second submit: %d", w.Code)
	}
	if len(*e.calls) != after || trxCount(e.cust) != before+1 {
		t.Errorf("second submit recharged: calls %d->%d", after, len(*e.calls))
	}

	// a wrong token after a fresh confirm: refused, and that token is spent too
	w = do(e.h, "POST", "/admin/customers/recharge", ids(e.cust), e.c)
	fresh := token(w.Body.String())
	w = do(e.h, "POST", "/admin/customers/recharge/run", runForm("00ff"), e.c)
	if w.Code != http.StatusConflict || len(*e.calls) != after {
		t.Errorf("wrong token: %d, calls %d->%d", w.Code, after, len(*e.calls))
	}
	w = do(e.h, "POST", "/admin/customers/recharge/run", runForm(fresh), e.c)
	if w.Code != http.StatusConflict || len(*e.calls) != after {
		t.Errorf("token reused after a wrong attempt: %d", w.Code)
	}

	// no token at all
	w = do(e.h, "POST", "/admin/customers/recharge/run", url.Values{}, e.c)
	if w.Code != http.StatusConflict {
		t.Errorf("no token: %d", w.Code)
	}

	// roles: Report is refused everywhere; Agent may confirm
	rita := login(t, e.h, "rita")
	wantCode(t, do(e.h, "POST", "/admin/customers/recharge", ids(e.cust), rita), 403, "confirm for Report")
	wantCode(t, do(e.h, "POST", "/admin/customers/recharge/run", runForm("x"), rita), 403, "run for Report")
	wantCode(t, do(e.h, "GET", bulkResultPath, nil, rita), 403, "result for Report")
	mk(t, e.q, "agnes", "Agent", 1)
	wantCode(t, do(e.h, "POST", "/admin/customers/recharge", ids(u2), login(t, e.h, "agnes")), 200, "confirm for Agent")

	// cap and empty selection at confirm: sent back to the list with an error, nothing charged
	many := url.Values{}
	for i := 0; i < bulkRechargeMax+1; i++ {
		many.Add("ids", fmt.Sprint(900000+i))
	}
	calls = len(*e.calls)
	for _, v := range []url.Values{many, {}} {
		w := do(e.h, "POST", "/admin/customers/recharge", v, e.c)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/customers" {
			t.Errorf("cap/empty: %d %s", w.Code, w.Header().Get("Location"))
		}
	}
	if len(*e.calls) != calls {
		t.Error("cap or empty selection reached the router")
	}
}
