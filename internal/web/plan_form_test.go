package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// planForm renders the new plan form for the admin, in English.
func planForm(t *testing.T, e *billEnv) string {
	t.Helper()
	do(e.h, "POST", "/admin/settings/localisation", url.Values{"language": {"english"}, "timezone": {"Asia/Jakarta"},
		"date_format": {"d-m-Y"}, "country_code_phone": {"62"}}, e.c)
	w := do(e.h, "GET", "/admin/plans/new", nil, e.c)
	if w.Code != http.StatusOK {
		t.Fatalf("new plan form: %d", w.Code)
	}
	return w.Body.String()
}

// TestPlanFormSectionOrder checks the cards come in the order: basic, price, end of plan, limits,
// network, then the folded router scripts.
func TestPlanFormSectionOrder(t *testing.T) {
	body := planForm(t, billApp(t))
	order := []string{`card-title">Basic<`, `card-title">Price &amp; validity<`, `card-title">When the plan ends<`,
		`card-title">Limits<`, `card-title">Network &amp; device<`, `<summary class="cursor-pointer`, `card-title">Router scripts<`}
	last := -1
	for _, s := range order {
		i := strings.Index(body, s)
		if i < 0 {
			t.Fatalf("section %q missing", s)
		}
		if i < last {
			t.Fatalf("section %q is out of order", s)
		}
		last = i
	}
}

// TestPlanFormServiceAndBillingLabels checks the two former "Type" fields have their own names.
func TestPlanFormServiceAndBillingLabels(t *testing.T) {
	body := planForm(t, billApp(t))
	for _, want := range []string{`for="type">Service`, `for="billing">Billing (Prepaid/Postpaid)`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing label %q", want)
		}
	}
	if strings.Contains(body, `>Plan Type<`) || strings.Contains(body, `for="type">Type<`) {
		t.Error("old Type or Plan Type label still shown")
	}
}

// TestPlanFormValidityInline checks number and unit sit on one line under one label.
func TestPlanFormValidityInline(t *testing.T) {
	body := planForm(t, billApp(t))
	start := strings.Index(body, `id="f-validity"`)
	end := strings.Index(body, `id="f-billing_day"`)
	if start < 0 || end < start {
		t.Fatal("validity row missing")
	}
	row := body[start+len(`id="f-validity"`) : end]
	if strings.Count(row, `name="validity"`) != 1 || strings.Count(row, `name="validity_unit"`) != 1 {
		t.Fatalf("validity row does not hold number and unit: %s", row)
	}
	if strings.Contains(row, `id="f-`) {
		t.Fatal("validity row spans more than one field")
	}
}

// planPost is the form a browser posts for a plan; the fields a hidden section leaves out are sent empty.
func planPost(name string, extra url.Values) url.Values {
	f := url.Values{"name": {name}, "type": {"Hotspot"}, "billing": {"prepaid"}, "price": {"5000"}, "validity": {"1"},
		"validity_unit": {"Days"}, "enabled": {"1"}, "audience": {"public"}, "device": {""}, "limited": {""},
		"limit_type": {""}, "time_limit": {""}, "time_unit": {"Hrs"}, "data_limit": {""}, "data_unit": {"GB"},
		"shared_users": {""}, "router_id": {""}, "pool_id": {""}, "expired_plan_id": {""}, "billing_day": {""},
		"on_login": {""}, "on_logout": {""}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

// savedPlan posts the form and returns the plan it stored.
func savedPlan(t *testing.T, e *billEnv, form url.Values) db.Plan {
	t.Helper()
	wantCode(t, do(e.h, "POST", "/admin/plans", form, e.c), 303, "save "+form.Get("name"))
	ps, err := e.q.ListPlans(t.Context(), db.ListPlansParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Name == form.Get("name") {
			return p
		}
	}
	t.Fatalf("plan %s not saved", form.Get("name"))
	return db.Plan{}
}

// The values a save stores must not change with the form layout: these are the stored columns per plan kind.
func TestPlanSaveStoresSameValues(t *testing.T) {
	e := billApp(t)
	pool, err := e.q.CreatePool(t.Context(), db.CreatePoolParams{Name: "pp", LocalIp: "10.0.0.1", RangeIp: "10.0.0.2-10.0.0.254", RouterID: e.rt})
	if err != nil {
		t.Fatal(err)
	}
	bw := url.Values{"bandwidth_id": {itoa(e.bw)}}
	with := func(kv url.Values) url.Values {
		f := planPost("", bw)
		for k, v := range kv {
			f[k] = v
		}
		return f
	}

	// Hotspot on RADIUS: no router, device Radius, data limit and device limit kept.
	radius := savedPlan(t, e, with(url.Values{"name": {"Radius"}, "device": {"Radius"}, "limited": {"1"},
		"limit_type": {"Data_Limit"}, "data_limit": {"5"}, "data_unit": {"GB"}, "shared_users": {"2"}}))
	if radius.Device != "Radius" || radius.RouterID.Valid || radius.PoolID.Valid || radius.Limited != 1 ||
		radius.LimitType.String != "Data_Limit" || radius.DataLimit.Int64 != 5 || radius.DataUnit.String != "GB" ||
		radius.SharedUsers.Int64 != 2 || !radius.BandwidthID.Valid || radius.BandwidthID.Int64 != e.bw || radius.Type != "Hotspot" {
		t.Fatalf("hotspot RADIUS plan stored as %+v", radius)
	}

	// PPPoE with a pool: empty device falls back to the PPPoE driver, the pool and router are kept.
	pppoe := savedPlan(t, e, with(url.Values{"name": {"Pppoe"}, "type": {"PPPoE"}, "router_id": {itoa(e.rt)},
		"pool_id": {itoa(pool.ID)}}))
	if pppoe.Device != "MikrotikPppoe" || pppoe.RouterID.Int64 != e.rt || !pppoe.RouterID.Valid ||
		pppoe.PoolID.Int64 != pool.ID || !pppoe.PoolID.Valid || pppoe.Limited != 0 || pppoe.Type != "PPPoE" {
		t.Fatalf("PPPoE plan with pool stored as %+v", pppoe)
	}

	// MikroTik API on a router: the chosen driver and router are stored, no pool.
	api := savedPlan(t, e, with(url.Values{"name": {"Api"}, "device": {"MikrotikHotspot"}, "router_id": {itoa(e.rt)}}))
	if api.Device != "MikrotikHotspot" || !api.RouterID.Valid || api.RouterID.Int64 != e.rt || api.PoolID.Valid {
		t.Fatalf("MikroTik API plan stored as %+v", api)
	}
}

// A MikroTik API plan without a router is still refused: the router is required for it.
func TestPlanSaveMikrotikNeedsRouter(t *testing.T) {
	e := billApp(t)
	form := planPost("", url.Values{"bandwidth_id": {itoa(e.bw)}, "name": {"NoRouter"}, "device": {"MikrotikHotspot"}})
	wantCode(t, do(e.h, "POST", "/admin/plans", form, e.c), http.StatusUnprocessableEntity, "mikrotik without router")
}

// A PPPoE data quota is accepted on RADIUS only; the MikroTik API cannot enforce it, and RADIUS
// takes bytes only.
func TestPlanSavePppoeQuota(t *testing.T) {
	e := billApp(t)
	q := func(name, dev, lt string) url.Values {
		return planPost("", url.Values{"bandwidth_id": {itoa(e.bw)}, "name": {name}, "type": {"PPPoE"}, "device": {dev},
			"router_id": {itoa(e.rt)}, "limited": {"1"}, "limit_type": {lt}, "data_limit": {"5"}, "data_unit": {"GB"}, "time_limit": {"1"}})
	}
	p := savedPlan(t, e, q("PQ", "Radius", "Data_Limit"))
	if p.Limited != 1 || p.LimitType.String != "Data_Limit" || p.DataLimit.Int64 != 5 {
		t.Fatalf("PPPoE RADIUS quota stored as %+v", p)
	}
	for _, c := range [][2]string{{"MikrotikPppoe", "Data_Limit"}, {"", "Data_Limit"}, {"Radius", "Time_Limit"}, {"Radius", "Both_Limit"}} {
		wantCode(t, do(e.h, "POST", "/admin/plans", q("Bad", c[0], c[1]), e.c), http.StatusUnprocessableEntity, "PPPoE quota "+c[0]+" "+c[1])
	}
}
