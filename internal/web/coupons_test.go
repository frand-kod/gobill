package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func cpnForm(code string) url.Values {
	return url.Values{"code": {code}, "type": {"percent"}, "value": {"10"}, "max_usage": {"1"}, "min_order": {"0"}, "max_discount": {"0"},
		"start_date": {"2020-01-01"}, "end_date": {"2099-12-31"}, "status": {"active"}}
}

func TestCouponAdminAndPortal(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()

	// Report role can read nothing and write nothing
	r := login(t, e.h, "rita")
	wantCode(t, do(e.h, "GET", "/admin/coupons", nil, r), 403, "report list")
	wantCode(t, do(e.h, "POST", "/admin/coupons", cpnForm("RITA"), r), 403, "report create")
	wantCode(t, do(e.h, "POST", "/admin/coupons/1/toggle", nil, r), 403, "report toggle")
	wantCode(t, do(e.h, "POST", "/admin/coupons/1/delete", nil, r), 403, "report delete")

	// admin CRUD; empty code gets a random one
	wantCode(t, do(e.h, "POST", "/admin/coupons", cpnForm("TEN"), e.c), 303, "create")
	wantCode(t, do(e.h, "POST", "/admin/coupons", cpnForm("TEN"), e.c), 422, "duplicate")
	wantCode(t, do(e.h, "POST", "/admin/coupons", cpnForm(""), e.c), 303, "random code")
	bad := cpnForm("BAD")
	bad.Set("end_date", "2019-01-01")
	wantCode(t, do(e.h, "POST", "/admin/coupons", bad, e.c), 422, "end before start")
	list, _ := e.q.SearchCoupons(ctx, db.SearchCouponsParams{PageLimit: 10})
	if len(list) != 2 {
		t.Fatalf("coupons %d", len(list))
	}
	ten, _ := e.q.GetCouponByCode(ctx, "TEN")
	wantCode(t, do(e.h, "POST", "/admin/coupons/"+itoa(ten.ID)+"/toggle", nil, e.c), 303, "toggle")
	if c, _ := e.q.GetCoupon(ctx, ten.ID); c.Status != "inactive" {
		t.Fatal("toggle did not block")
	}
	wantCode(t, do(e.h, "POST", "/admin/coupons/"+itoa(ten.ID)+"/toggle", nil, e.c), 303, "untoggle")

	// portal: coupon discounts the balance order and bumps usage
	portalCust(t, e, 9000)
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "enable_balance", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t, "Gold", "PPPoE", 10000)
	c, _ := custLogin(t, e, "u1", "pw12345")
	buy := "/portal/plans/" + itoa(p.ID) + "/balance"
	if w := do(e.h, "POST", buy, url.Values{"coupon": {"NOPE"}}, c); w.Code != 200 || !strings.Contains(w.Body.String(), "alert-error") {
		t.Fatalf("unknown coupon: %d", w.Code)
	}
	wantCode(t, do(e.h, "POST", buy, url.Values{"coupon": {"TEN"}}, c), http.StatusSeeOther, "buy with coupon")
	trx, _ := e.q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	cu, _ := e.q.GetCustomer(ctx, e.cust.ID)
	if len(trx) != 1 || trx[0].Price != 9000 || trx[0].Note != "Coupon TEN" || cu.Balance != 0 {
		t.Fatalf("trx %+v balance %d", trx, cu.Balance)
	}
	if u, _ := e.q.GetCoupon(ctx, ten.ID); u.Used != 1 {
		t.Fatalf("used %d", u.Used)
	}
}
