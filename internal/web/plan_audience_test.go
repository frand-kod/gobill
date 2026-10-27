package web

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// Customer side of the plan audience: admin-only and Rp 0 plans are not listed, and a direct POST
// (balance, Tripay, friend) to them is refused without touching the balance or creating an order.
func TestPlanAudienceCustomerRefused(t *testing.T) {
	e := payApp(t) // customer u1 logged in (e.cookie), Tripay on, plan "Home" 100000 public
	ctx := t.Context()
	mk := func(name string, price int64, aud string) db.Plan {
		p, err := e.q.CreatePlan(ctx, db.CreatePlanParams{Name: name, Type: "PPPoE", Billing: "prepaid", Price: price, Validity: 1,
			ValidityUnit: "Days", Enabled: 1, Audience: aud, BandwidthID: sql.NullInt64{Int64: e.bw, Valid: true}, RouterID: sql.NullInt64{Int64: e.rt, Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	staff, zero := mk("Staf Khusus", 10000, "admin"), mk("Teknisi Nol", 0, "public")
	if _, err := e.q.AdjustBalance(ctx, db.AdjustBalanceParams{Delta: 500000, ID: e.cust.ID}); err != nil {
		t.Fatal(err)
	}
	const msg = "Paket ini hanya bisa diberikan admin"

	list := do(e.h, "GET", "/portal/plans", nil, e.cookie).Body.String()
	if !strings.Contains(list, "Home") || strings.Contains(list, "Staf Khusus") || strings.Contains(list, "Teknisi Nol") {
		t.Fatal("portal list shows admin or Rp 0 plans, or hides the public one")
	}

	for _, p := range []db.Plan{staff, zero} {
		id := itoa(p.ID)
		w := do(e.h, "POST", "/portal/plans/"+id+"/balance", nil, e.cookie)
		if w.Code != 200 || !strings.Contains(w.Body.String(), msg) {
			t.Fatalf("balance %s: %d", p.Name, w.Code)
		}
		w = do(e.h, "POST", "/portal/plans/"+id+"/pay", url.Values{"channel": {"QRIS"}}, e.cookie)
		if w.Code != 200 || !strings.Contains(w.Body.String(), msg) {
			t.Fatalf("tripay %s: %d", p.Name, w.Code)
		}
		w = do(e.h, "POST", "/portal/plans/"+id+"/friend", url.Values{"username": {"u2"}}, e.cookie)
		if w.Code != 200 || !strings.Contains(w.Body.String(), msg) {
			t.Fatalf("friend %s: %d", p.Name, w.Code)
		}
		if w := do(e.h, "GET", "/portal/plans/"+id+"/friend", nil, e.cookie); w.Code != http.StatusNotFound {
			t.Fatalf("friend form %s: %d", p.Name, w.Code)
		}
	}
	var orders, subs int
	e.s.conn.QueryRow("SELECT COUNT(*) FROM payment_requests").Scan(&orders)
	e.s.conn.QueryRow("SELECT COUNT(*) FROM subscriptions").Scan(&subs)
	if c, _ := e.q.GetCustomer(ctx, e.cust.ID); c.Balance != 500000 || orders != 0 || subs != 0 {
		t.Fatalf("refused order changed state: balance %d orders %d", c.Balance, orders)
	}
}

// Admin side: the plan form saves the audience and the list marks admin plans.
func TestPlanAudienceAdminForm(t *testing.T) {
	e := billApp(t)
	form := url.Values{"name": {"Staf"}, "type": {"PPPoE"}, "billing": {"prepaid"}, "price": {"0"}, "validity": {"1"},
		"validity_unit": {"Days"}, "bandwidth_id": {itoa(e.bw)}, "router_id": {itoa(e.rt)}, "enabled": {"1"}, "audience": {"admin"}}
	wantCode(t, do(e.h, "POST", "/admin/plans", form, e.c), 303, "create admin plan")
	p, err := e.q.SearchPlans(t.Context(), db.SearchPlansParams{Q: "Staf", PageLimit: 1})
	if err != nil || len(p) != 1 || p[0].Audience != "admin" || p[0].Price != 0 {
		t.Fatalf("saved plan: %+v %v", p, err)
	}
	list := do(e.h, "GET", "/admin/plans", nil, e.c).Body.String()
	if !strings.Contains(list, "Admin only") && !strings.Contains(list, "Khusus admin") {
		t.Fatal("admin list does not mark the admin plan")
	}
	bad := url.Values{"name": {"Salah"}, "type": {"PPPoE"}, "billing": {"prepaid"}, "price": {"1"}, "validity": {"1"},
		"validity_unit": {"Days"}, "bandwidth_id": {itoa(e.bw)}, "router_id": {itoa(e.rt)}, "audience": {"vip"}}
	wantCode(t, do(e.h, "POST", "/admin/plans", bad, e.c), 422, "invalid audience")
}

// FUP bandwidth: saved only for a RADIUS plan with a data limit, dropped otherwise.
func TestPlanFUPSave(t *testing.T) {
	e := billApp(t)
	save := func(name, device, limitType string) db.Plan {
		form := url.Values{"name": {name}, "type": {"Hotspot"}, "billing": {"prepaid"}, "price": {"1000"}, "validity": {"1"},
			"validity_unit": {"Days"}, "bandwidth_id": {itoa(e.bw)}, "fup_bandwidth_id": {itoa(e.bw)}, "router_id": {itoa(e.rt)},
			"device": {device}, "enabled": {"1"}, "limited": {"1"}, "limit_type": {limitType}, "data_limit": {"5"}, "data_unit": {"GB"}}
		wantCode(t, do(e.h, "POST", "/admin/plans", form, e.c), 303, "create "+name)
		p, err := e.q.SearchPlans(t.Context(), db.SearchPlansParams{Q: name, PageLimit: 1})
		if err != nil || len(p) != 1 {
			t.Fatalf("%s: %v", name, err)
		}
		return p[0]
	}
	if p := save("FupOn", "Radius", "Data_Limit"); !p.FupBandwidthID.Valid || p.FupBandwidthID.Int64 != e.bw {
		t.Fatalf("RADIUS data plan: fup %+v", p.FupBandwidthID)
	}
	if p := save("FupDummy", "Dummy", "Data_Limit"); p.FupBandwidthID.Valid {
		t.Fatal("non-RADIUS plan must not keep a FUP")
	}
}
