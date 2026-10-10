package web

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestRadiusPlanWithoutRouter(t *testing.T) {
	e := billApp(t)
	form := url.Values{"name": {"Radius"}, "type": {"PPPoE"}, "billing": {"prepaid"}, "price": {"1000"}, "validity": {"1"},
		"validity_unit": {"Days"}, "bandwidth_id": {itoa(e.bw)}, "device": {"Radius"}, "enabled": {"1"}}
	wantCode(t, do(e.h, "POST", "/admin/plans", form, e.c), 303, "radius plan without router")
	pl, _ := e.q.ListPlans(t.Context(), db.ListPlansParams{Limit: 10})
	if len(pl) != 1 || pl[0].RouterID.Valid || pl[0].Device != "Radius" {
		t.Fatalf("plans %+v", pl)
	}
}

func TestSubscriptionCSVAndSync(t *testing.T) {
	e := billApp(t)
	gold, silver := e.plan(t, "gold", "PPPoE", 10000), e.plan(t, "silver", "PPPoE", 20000)
	u2, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "U Two", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for _, s := range []db.CreateSubscriptionParams{
		{CustomerID: e.cust.ID, PlanID: gold.ID, RouterID: sql.NullInt64{Int64: e.rt, Valid: true}, Type: "PPPoE", StartedAt: now, ExpiresAt: now + 86400},
		{CustomerID: u2.ID, PlanID: silver.ID, RouterID: sql.NullInt64{Int64: e.rt, Valid: true}, Type: "PPPoE", StartedAt: now, ExpiresAt: now + 86400},
	} {
		if _, err := e.q.CreateSubscription(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	w := do(e.h, "GET", "/admin/subscriptions/export?plan="+itoa(gold.ID), nil, e.c)
	if b := w.Body.String(); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(b, "gold") || strings.Contains(b, "silver") {
		t.Fatalf("csv: %d\n%s", w.Code, b)
	}

	// sync re-sends the customer to the device (recDev records AddCustomer)
	wantCode(t, do(e.h, "POST", "/admin/customers/"+itoa(e.cust.ID)+"/recharge", url.Values{"plan": {itoa(gold.ID)}, "method": {"Cash"}}, e.c), 303, "recharge")
	subs, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 5})
	*e.calls = nil
	wantCode(t, do(e.h, "POST", "/admin/subscriptions/"+itoa(subs[0].ID)+"/sync", nil, e.c), 303, "sync")
	if len(*e.calls) != 1 || (*e.calls)[0] != "customer u1" {
		t.Fatalf("device calls: %v", *e.calls)
	}
}

func TestDepositForm(t *testing.T) {
	e := billApp(t)
	top := e.plan(t, "topup", "Balance", 50000)
	f := url.Values{"customer": {"u1"}, "amount": {"7000"}, "note": {"cash in"}}
	wantCode(t, do(e.h, "POST", "/admin/deposit", url.Values{"customer": {"u1"}}, e.c), 422, "no plan or amount")
	wantCode(t, do(e.h, "POST", "/admin/deposit", f, e.c), 303, "amount deposit")
	wantCode(t, do(e.h, "POST", "/admin/deposit", url.Values{"customer": {"u1"}, "plan": {itoa(top.ID)}}, e.c), 303, "plan deposit")
	c, _ := e.q.GetCustomer(t.Context(), e.cust.ID)
	trx, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 10})
	if c.Balance != 57000 || len(trx) != 2 || trx[1].Note != "cash in" || trx[1].Type != "Balance" {
		t.Fatalf("balance %d trx %+v", c.Balance, trx)
	}
}

func TestSubscriptionAdmin(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	wantCode(t, do(e.h, "POST", "/admin/customers/"+itoa(e.cust.ID)+"/recharge", url.Values{"plan": {itoa(p.ID)}, "method": {"Cash"}}, e.c), 303, "recharge")
	subs, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 5})
	base := "/admin/subscriptions/" + itoa(subs[0].ID)
	if w := do(e.h, "GET", "/admin/subscriptions?status=active&q=u1", nil, e.c); !strings.Contains(w.Body.String(), "day") {
		t.Fatal("list should show the subscription")
	}
	if w := do(e.h, "GET", base+"/edit", nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), `type="datetime-local"`) {
		t.Fatal("edit form")
	}

	// edit: the new expiry is read in the billing zone (UTC here) and persisted
	want := time.Now().UTC().AddDate(0, 0, 20).Truncate(time.Minute)
	wantCode(t, do(e.h, "POST", base, url.Values{"plan": {itoa(p.ID)}, "expires_at": {want.Format(localTime)}}, e.c), 303, "edit")
	if got, _ := e.q.GetSubscription(t.Context(), subs[0].ID); got.ExpiresAt != want.Unix() {
		t.Fatalf("expiry %v, want %v", time.Unix(got.ExpiresAt, 0), want)
	}
	wantCode(t, do(e.h, "POST", base, url.Values{"plan": {itoa(p.ID)}, "expires_at": {"nope"}}, e.c), 422, "bad date")

	wantCode(t, do(e.h, "POST", base+"/extend", url.Values{"days": {"5"}}, e.c), 303, "extend")
	if got, _ := e.q.GetSubscription(t.Context(), subs[0].ID); got.ExpiresAt != want.AddDate(0, 0, 5).Unix() {
		t.Fatalf("extend: %v", time.Unix(got.ExpiresAt, 0))
	}

	for range 2 {
		wantCode(t, do(e.h, "POST", base+"/deactivate", nil, e.c), 303, "deactivate")
	}
	if got, _ := e.q.GetSubscription(t.Context(), subs[0].ID); got.Status != "expired" {
		t.Fatalf("status %s", got.Status)
	}
}

func TestCustomerCSVFiltered(t *testing.T) {
	e := billApp(t)
	for _, c := range []db.CreateCustomerParams{
		{Username: "hot1", PasswordHash: "h", Fullname: "Hot One", ServiceType: "Hotspot", Status: "Active"},
		{Username: "hot2", PasswordHash: "h", Fullname: "Hot Two", ServiceType: "Hotspot", Status: "Banned"},
	} {
		if _, err := e.q.CreateCustomer(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	w := do(e.h, "GET", "/admin/customers/export?service_type=Hotspot&status=Active", nil, e.c)
	b := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(b, "hot1") || strings.Contains(b, "hot2") || strings.Contains(b, "u1,") {
		t.Fatalf("csv: %d\n%s", w.Code, b)
	}
	if w := do(e.h, "GET", "/admin/customers?sort=balance&dir=desc&service_type=PPPoE", nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), "u1") {
		t.Fatal("sorted list")
	}
}

func TestReportRoleCannotPostBilling(t *testing.T) {
	e := billApp(t)
	r := login(t, e.h, "rita")
	for path, f := range map[string]url.Values{
		"/admin/deposit":                    {"customer": {"u1"}, "amount": {"1000"}},
		"/admin/subscriptions/1":            {"plan": {"1"}, "expires_at": {"2030-01-01T00:00"}},
		"/admin/subscriptions/1/extend":     {"days": {"1"}},
		"/admin/subscriptions/1/deactivate": nil,
	} {
		wantCode(t, do(e.h, "POST", path, f, r), 403, "POST "+path)
	}
	if c, _ := e.q.GetCustomer(t.Context(), e.cust.ID); c.Balance != 0 {
		t.Fatal("report role changed the balance")
	}
}

func TestAdminExtendSetting(t *testing.T) {
	e := billApp(t)
	set := func(v string) {
		if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "admin_extend", Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	set("off")
	wantCode(t, do(e.h, "POST", "/admin/subscriptions/1/extend", url.Values{"days": {"1"}}, e.c), 403, "extend while off")
	if strings.Contains(do(e.h, "GET", "/admin/subscriptions", nil, e.c).Body.String(), `/extend"`) {
		t.Fatal("extend button shown while off")
	}
	set("super")
	if w := do(e.h, "POST", "/admin/subscriptions/1/extend", url.Values{"days": {"1"}}, e.c); w.Code == 403 {
		t.Fatal("SuperAdmin refused with admin_extend=super")
	}
}
