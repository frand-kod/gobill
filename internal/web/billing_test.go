package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/device"
)

type billEnv struct {
	h      http.Handler
	q      *db.Queries
	c      *http.Cookie
	cust   db.Customer
	bw, rt int64
	calls  *[]string
}

type recDev struct {
	device.Dummy
	calls *[]string
}

func (d recDev) AddPlan(_ context.Context, p device.Plan) error {
	*d.calls = append(*d.calls, "add "+p.Name)
	return nil
}

func (d recDev) AddCustomer(_ context.Context, c device.Customer, _ device.Plan) error {
	*d.calls = append(*d.calls, "customer "+c.Username)
	return nil
}

func billApp(t *testing.T) *billEnv {
	s, h, q, c := crudApp(t)
	e := &billEnv{h: h, q: q, c: c, calls: new([]string)}
	s.Billing = &billing.Service{DB: s.conn, Q: q, Key: s.SecretKey,
		DeviceFor: func(db.Plan, db.Router) (device.Device, error) { return recDev{calls: e.calls}, nil }}
	ctx := t.Context()
	bw, err := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := q.CreateRouter(ctx, db.CreateRouterParams{Name: "r", Host: "h", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	e.bw, e.rt = bw.ID, rt.ID
	e.cust, err = q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "u1", PasswordHash: "h", Fullname: "U One", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *billEnv) plan(t *testing.T, name, typ string, price int64) db.Plan {
	p := db.CreatePlanParams{Name: name, Type: typ, Billing: "prepaid", Price: price, Validity: 1, ValidityUnit: "Days", Enabled: 1}
	if typ != "Balance" {
		p.BandwidthID, p.RouterID = sql.NullInt64{Int64: e.bw, Valid: true}, sql.NullInt64{Int64: e.rt, Valid: true}
	}
	pl, err := e.q.CreatePlan(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

func TestPlanCRUD(t *testing.T) {
	e := billApp(t)
	form := url.Values{"name": {"Home"}, "type": {"PPPoE"}, "billing": {"prepaid"}, "price": {"150000"}, "validity": {"1"},
		"validity_unit": {"Months"}, "bandwidth_id": {itoa(e.bw)}, "router_id": {itoa(e.rt)}, "enabled": {"1"}}
	// router is required for non-Balance plans
	bad := url.Values{"name": {"X"}, "type": {"PPPoE"}, "billing": {"prepaid"}, "price": {"1"}, "validity": {"1"}, "validity_unit": {"Days"}}
	wantCode(t, do(e.h, "POST", "/admin/plans", bad, e.c), 422, "plan without router")

	wantCode(t, do(e.h, "POST", "/admin/plans", form, e.c), 303, "create")
	if len(*e.calls) != 1 || (*e.calls)[0] != "add Home" {
		t.Fatalf("device calls: %v", *e.calls)
	}
	list, _ := e.q.ListPlans(t.Context(), db.ListPlansParams{Limit: 10})
	if len(list) != 1 || list[0].Device != "MikrotikPppoe" || list[0].Price != 150000 {
		t.Fatalf("plans: %+v", list)
	}

	// Balance plan: no router/bandwidth, no device call
	bal := url.Values{"name": {"Topup"}, "type": {"Balance"}, "billing": {"prepaid"}, "price": {"50000"}, "validity_unit": {"Days"}, "enabled": {"1"}}
	wantCode(t, do(e.h, "POST", "/admin/plans", bal, e.c), 303, "create balance plan")
	if len(*e.calls) != 1 {
		t.Fatalf("balance plan touched the device: %v", *e.calls)
	}
	if p, err := e.q.GetPlan(t.Context(), list[0].ID+1); err != nil || p.Type != "Balance" || p.RouterID.Valid {
		t.Fatalf("balance plan: %+v %v", p, err)
	}

	id := "/admin/plans/" + itoa(list[0].ID)
	form.Set("name", "Home2")
	wantCode(t, do(e.h, "POST", id, form, e.c), 303, "update")
	if w := do(e.h, "GET", id+"/edit", nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), `value="Home2"`) {
		t.Fatal("edit form")
	}
	if w := do(e.h, "GET", "/admin/plans?q=Home2", nil, e.c); !strings.Contains(w.Body.String(), "Rp 150.000") {
		t.Fatal("list should show formatted price")
	}
	wantCode(t, do(e.h, "POST", id+"/delete", nil, e.c), 303, "delete")
	if _, err := e.q.GetPlan(t.Context(), list[0].ID); err != sql.ErrNoRows {
		t.Fatal("not deleted")
	}
}

func TestRechargeForm(t *testing.T) {
	e := billApp(t)
	cash := e.plan(t, "day", "PPPoE", 10000)
	pay := "/admin/customers/" + itoa(e.cust.ID) + "/recharge"

	wantCode(t, do(e.h, "POST", pay, url.Values{"plan": {itoa(cash.ID)}, "method": {"Cash"}}, e.c), 303, "recharge")
	subs, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	trx, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	if len(subs) != 1 || len(trx) != 1 || trx[0].Price != 10000 || trx[0].Method != "Admin - Cash" {
		t.Fatalf("subs=%+v trx=%+v", subs, trx)
	}

	// no balance: friendly error, nothing new recorded
	wantCode(t, do(e.h, "POST", pay, url.Values{"plan": {itoa(cash.ID)}, "method": {"Balance"}}, e.c), 303, "balance recharge")
	w := do(e.h, "GET", "/admin/customers/"+itoa(e.cust.ID), nil, e.c)
	body := w.Body.String()
	if !strings.Contains(body, "Saldo tidak cukup") {
		t.Fatalf("no insufficient balance error:\n%s", body)
	}
	if !strings.Contains(body, "Rp 10.000") || !strings.Contains(body, "Aktif") {
		t.Fatal("detail should list the transaction and subscription")
	}
	if trx, _ = e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10}); len(trx) != 1 {
		t.Fatal("failed balance recharge created a transaction")
	}
	if w := do(e.h, "GET", "/admin/transactions?q=u1", nil, e.c); !strings.Contains(w.Body.String(), "Admin - Cash") {
		t.Fatal("transactions list")
	}
}

func TestVouchers(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	gen := url.Values{"plan": {itoa(p.ID)}, "numbervoucher": {"5"}, "voucher_format": {"up"}, "prefix": {"NX-"}, "lengthcode": {"8"}}
	wantCode(t, do(e.h, "POST", "/admin/vouchers", url.Values{"plan": {itoa(p.ID)}, "numbervoucher": {"0"}, "voucher_format": {"up"}, "lengthcode": {"8"}}, e.c), 422, "bad count")
	wantCode(t, do(e.h, "POST", "/admin/vouchers", gen, e.c), 303, "generate")
	vs, _ := e.q.ListVouchers(t.Context(), db.ListVouchersParams{Limit: 50})
	seen := map[string]bool{}
	for _, v := range vs {
		if !strings.HasPrefix(v.Code, "NX-") || len(v.Code) != 11 {
			t.Fatalf("code %q", v.Code)
		}
		seen[v.Code] = true
	}
	if len(vs) != 5 || len(seen) != 5 {
		t.Fatalf("want 5 unique vouchers, got %d/%d", len(vs), len(seen))
	}

	// redeem once only
	code := vs[0].Code
	red := url.Values{"customer": {"u1"}, "code": {code}}
	wantCode(t, do(e.h, "POST", "/admin/vouchers/redeem", red, e.c), 303, "redeem")
	if w := do(e.h, "POST", "/admin/vouchers/redeem", red, e.c); w.Code != 422 || !strings.Contains(w.Body.String(), "Voucher tidak valid atau sudah dipakai") {
		t.Fatalf("second redeem: %d", w.Code)
	}
	if trx, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10}); len(trx) != 1 {
		t.Fatalf("want 1 transaction, got %d", len(trx))
	}

	// status filter and print page
	w := do(e.h, "GET", "/admin/vouchers?status=used", nil, e.c)
	if !strings.Contains(w.Body.String(), code) || strings.Contains(w.Body.String(), vs[1].Code) {
		t.Fatal("status filter")
	}
	w = do(e.h, "GET", "/admin/vouchers/print?plan="+itoa(p.ID)+"&limit=10", nil, e.c)
	if w.Code != 200 || strings.Count(w.Body.String(), "data:image/png;base64,") != 4 || strings.Contains(w.Body.String(), code) {
		t.Fatalf("print page: %d, qr=%d", w.Code, strings.Count(w.Body.String(), "data:image/png;base64,"))
	}

	// delete unused only
	wantCode(t, do(e.h, "POST", "/admin/vouchers/"+itoa(vs[1].ID)+"/delete", nil, e.c), 303, "delete")
	if _, err := e.q.GetVoucher(t.Context(), vs[1].ID); err != sql.ErrNoRows {
		t.Fatal("unused voucher not deleted")
	}
	wantCode(t, do(e.h, "POST", "/admin/vouchers/"+itoa(vs[0].ID)+"/delete", nil, e.c), 303, "delete used")
	if _, err := e.q.GetVoucher(t.Context(), vs[0].ID); err != nil {
		t.Fatal("used voucher must stay")
	}
}

func TestReportRoleBillingReadOnly(t *testing.T) {
	e := billApp(t)
	r := login(t, e.h, "rita")
	p := e.plan(t, "day", "PPPoE", 10000)
	for _, path := range []string{"/admin/vouchers", "/admin/transactions"} {
		wantCode(t, do(e.h, "GET", path, nil, r), 200, path)
	}
	for _, path := range []string{"/admin/plans", "/admin/vouchers/new", "/admin/vouchers/redeem"} {
		wantCode(t, do(e.h, "GET", path, nil, r), 403, "GET "+path)
	}
	for path, f := range map[string]url.Values{
		"/admin/plans":             {"name": {"x"}},
		"/admin/vouchers":          {"plan": {itoa(p.ID)}, "numbervoucher": {"1"}, "voucher_format": {"up"}, "lengthcode": {"8"}},
		"/admin/vouchers/redeem":   {"customer": {"u1"}, "code": {"x"}},
		"/admin/vouchers/1/delete": nil,
		"/admin/customers/" + itoa(e.cust.ID) + "/recharge": {"plan": {itoa(p.ID)}, "method": {"Cash"}},
	} {
		wantCode(t, do(e.h, "POST", path, f, r), 403, "POST "+path)
	}
	if n, _ := e.q.ListVouchers(t.Context(), db.ListVouchersParams{Limit: 5}); len(n) != 0 {
		t.Fatal("report role created vouchers")
	}
}
