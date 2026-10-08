package web

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestDepositForm(t *testing.T) {
	e := billApp(t)
	top := e.plan(t, "topup", "Balance", 50000)
	f := url.Values{"customer": {"u1"}, "amount": {"7000"}, "note": {"cash in"}}
	wantCode(t, do(e.h, "POST", "/admin/deposit", url.Values{"customer": {"u1"}}, e.c), 422, "no plan or amount")
	wantCode(t, do(e.h, "POST", "/admin/deposit", f, e.c), 303, "amount deposit")
	wantCode(t, do(e.h, "POST", "/admin/deposit", url.Values{"customer": {"u1"}, "plan": {itoa(top.ID)}}, e.c), 303, "plan deposit")
	c, _ := e.q.GetCustomer(t.Context(), e.cust.ID)
	trx, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: c.ID, Limit: 10})
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
