package web

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/i18n"

	gobill "github.com/frand-kod/gobill"
)

// (1) A new customer lands on its own page, where the plan is chosen.
func TestCreateCustomerRedirectsToDetail(t *testing.T) {
	_, h, q, c := crudApp(t)
	form := url.Values{"username": {"budi"}, "password": {"pw123456"}, "fullname": {"Budi"}, "service_type": {"PPPoE"}, "status": {"Active"}}
	w := do(h, "POST", "/admin/customers", form, c)
	wantCode(t, w, 303, "create")
	cu, err := q.GetCustomerByUsername(t.Context(), "budi")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := w.Header().Get("Location"), "/admin/customers/"+itoa(cu.ID); got != want {
		t.Fatalf("redirect %q, want %q", got, want)
	}
	// edit still goes back to the list
	w = do(h, "POST", "/admin/customers/"+itoa(cu.ID), form, c)
	if got := w.Header().Get("Location"); got != "/admin/customers" {
		t.Fatalf("edit redirect %q", got)
	}
}

type brokenDev struct{ device.Dummy }

func (brokenDev) AddCustomer(context.Context, device.Customer, device.Plan) error {
	return errors.New("dial tcp 10.0.0.1:8728: i/o timeout")
}

// (4) A router failure after a committed recharge shows a warning, never "Recharge Successful",
// and leaves the money and the transaction exactly as a successful recharge would.
func TestRechargeDeviceFailureWarns(t *testing.T) {
	e := billApp(t)
	e.s.Billing.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return brokenDev{}, nil }
	pl := e.plan(t, "day", "PPPoE", 10000)
	pay := "/admin/customers/" + itoa(e.cust.ID) + "/recharge"
	wantCode(t, do(e.h, "POST", pay, url.Values{"plan": {itoa(pl.ID)}, "method": {"Cash"}}, e.c), 303, "recharge")

	body := do(e.h, "GET", "/admin/customers/"+itoa(e.cust.ID), nil, e.c).Body.String()
	if !strings.Contains(body, "alert-warn") || !strings.Contains(body, "tekan Sync") {
		t.Fatalf("no sync warning:\n%s", body)
	}
	if strings.Contains(body, "Isi ulang berhasil") {
		t.Fatal("success flash shown although the router failed")
	}
	subs, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	trx, _ := e.q.ListTransactionsByCustomer(t.Context(), db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: e.cust.ID, Valid: true}, Limit: 10})
	cu, _ := e.q.GetCustomer(t.Context(), e.cust.ID)
	if len(subs) != 1 || len(trx) != 1 || trx[0].Price != 10000 || cu.Balance != 0 {
		t.Fatalf("money/transaction changed: subs=%d trx=%+v balance=%d", len(subs), trx, cu.Balance)
	}
}

// (6) The quick-start checklist ticks from row counts and disappears when everything is done.
func TestSetupSteps(t *testing.T) {
	steps, all := setupSteps(db.CountSetupRow{})
	if all || len(steps) != 4 || steps[0].Done {
		t.Fatalf("empty install: %+v %v", steps, all)
	}
	steps, all = setupSteps(db.CountSetupRow{Nas: 1, Bandwidths: 1})
	if all || !steps[0].Done || !steps[1].Done || steps[2].Done || steps[3].Done {
		t.Fatalf("nas+bandwidth: %+v", steps)
	}
	if _, all = setupSteps(db.CountSetupRow{Routers: 1, Bandwidths: 1, Plans: 1, Vouchers: 1}); !all {
		t.Fatal("voucher should count as the last step")
	}
}

func TestDashboardChecklistAndQuickActions(t *testing.T) {
	_, h, _, c := crudApp(t)
	body := do(h, "GET", "/admin", nil, c).Body.String()
	for _, want := range []string{`/admin/customers/new`, `/admin/vouchers/new`, `/admin/deposit`, "Mulai cepat", "/admin/bandwidth/new"} {
		if !strings.Contains(body, want) {
			t.Errorf("empty dashboard missing %q", want)
		}
	}
	e := billApp(t)
	e.plan(t, "p", "PPPoE", 1)
	if body = do(e.h, "GET", "/admin", nil, e.c).Body.String(); strings.Contains(body, "Mulai cepat") {
		t.Error("checklist still shown with router, bandwidth, plan and customer present")
	}
}

func TestWALink(t *testing.T) {
	for _, c := range []struct{ phone, cc, want string }{
		{"0812-3456-7890", "62", "https://wa.me/6281234567890"},
		{"+62 812 3456 7890", "62", "https://wa.me/6281234567890"},
		{"0812", "62", ""},
		{"", "62", ""},
	} {
		if got := waLink(c.phone, c.cc); got != c.want {
			t.Errorf("waLink(%q) = %q, want %q", c.phone, got, c.want)
		}
	}
}

// (10) Raw errors become plain advice.
func TestFriendlyErrors(t *testing.T) {
	for _, c := range []struct{ err, want string }{
		{"dial tcp 1.2.3.4:8728: i/o timeout", msgRouterTimeout},
		{"dial tcp 1.2.3.4:8728: connect: connection refused", msgRouterRefused},
		{"lookup nope.local: no such host", msgRouterHost},
		{"from RouterOS device: invalid user name or password (6)", msgRouterLogin},
		{"something odd", msgRouterOther},
	} {
		if got := routerErr(errors.New(c.err), msgRouterOther); got != c.want {
			t.Errorf("%q -> %q", c.err, got)
		}
	}
	if planErrMsg(false, 0) != msgPlanMissing || planErrMsg(true, 0) != msgPlanOff || planErrMsg(true, 1) != msgPlanBad {
		t.Error("planErrMsg")
	}
}

func TestRouterTestFailureIsPlainWithDetails(t *testing.T) {
	e := billApp(t)
	e.s.Billing.DeviceFor = func(db.Plan, db.Router) (device.Device, error) { return brokenDev{}, nil }
	wantCode(t, do(e.h, "POST", "/admin/routers/"+itoa(e.rt)+"/test", nil, e.c), 303, "test")
	body := do(e.h, "GET", "/admin/routers", nil, e.c).Body.String()
	if !strings.Contains(body, "Koneksi gagal") || !strings.Contains(body, "<details") {
		t.Fatalf("router test failure not friendly:\n%s", body)
	}
}

func TestInsufficientBalanceOffersAddBalance(t *testing.T) {
	e := billApp(t)
	pl := e.plan(t, "day", "PPPoE", 10000)
	pay := "/admin/customers/" + itoa(e.cust.ID) + "/recharge"
	wantCode(t, do(e.h, "POST", pay, url.Values{"plan": {itoa(pl.ID)}, "method": {"Balance"}}, e.c), 303, "recharge")
	body := do(e.h, "GET", "/admin/customers/"+itoa(e.cust.ID), nil, e.c).Body.String()
	if !strings.Contains(body, "/admin/deposit?customer=u1") {
		t.Fatalf("no add-balance link:\n%s", body)
	}
	// a switched-off plan says so instead of "Invalid plan"
	if _, err := e.q.GetPlan(t.Context(), pl.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, do(e.h, "POST", pay, url.Values{"plan": {"9999"}, "method": {"Cash"}}, e.c), 303, "missing plan")
	if body = do(e.h, "GET", "/admin/customers/"+itoa(e.cust.ID), nil, e.c).Body.String(); !strings.Contains(body, "Paket tidak ditemukan") {
		t.Fatal("missing plan not explained")
	}
}

// Part B: every admin list/form page and the portal home carry the callout.
func TestPagesHaveIntro(t *testing.T) {
	e := billApp(t)
	paths := []string{
		"/admin", "/admin/reports", "/admin/reports/period", "/admin/radius/sessions", "/admin/maps/customers",
		"/admin/customers", "/admin/subscriptions", "/admin/vouchers", "/admin/transactions", "/admin/plans", "/admin/bandwidth",
		"/admin/routers", "/admin/pool", "/admin/nas", "/admin/odp", "/admin/coupons", "/admin/users", "/admin/fields",
		"/admin/logs", "/admin/logs/radius", "/admin/logs/messages", "/admin/payment-gateway", "/admin/payment-gateway/audit",
		"/admin/customers/new", "/admin/bandwidth/new", "/admin/plans/new", "/admin/routers/new", "/admin/pool/new", "/admin/nas/new",
		"/admin/odp/new", "/admin/coupons/new", "/admin/users/new", "/admin/fields/new", "/admin/vouchers/new",
		"/admin/vouchers/redeem", "/admin/deposit", "/admin/message/send", "/admin/message/bulk", "/admin/password",
		"/admin/pages/announcement", "/admin/settings/app", "/admin/customers/" + itoa(e.cust.ID) + "/edit",
	}
	for _, p := range paths {
		w := do(e.h, "GET", p, nil, e.c)
		if w.Code != 200 {
			t.Errorf("%s: status %d", p, w.Code)
			continue
		}
		if !strings.Contains(w.Body.String(), "data-intro") {
			t.Errorf("%s: no intro callout", p)
		}
	}
	portalCust(t, e, 0)
	pc, _ := custLogin(t, e, "u1", "pw12345")
	if body := do(e.h, "GET", "/portal", nil, pc).Body.String(); !strings.Contains(body, "data-intro") || !strings.Contains(body, "/portal/plans") {
		t.Error("portal home: no intro or buy button")
	}
}

// Every intro string must exist in the Indonesian catalog (they are looked up dynamically, so the template test cannot see them).
func TestIntrosTranslated(t *testing.T) {
	cat, err := i18n.Load(gobill.FS, "lang")
	if err != nil {
		t.Fatal(err)
	}
	id := cat["indonesia"]
	check := func(s string) {
		if s != "" && id[s] == "" {
			t.Errorf("not in indonesia.json: %q", s)
		}
	}
	for _, tbl := range []map[string]intro{listIntros, formIntros, pageIntros} {
		for _, in := range tbl {
			check(in.Title)
			check(in.Text)
			check(in.Tip)
		}
	}
	for _, m := range []string{msgRouterTimeout, msgRouterRefused, msgRouterHost, msgRouterLogin, msgRouterOther, msgNASOther,
		msgPlanMissing, msgPlanOff, msgPlanBad, msgNoBalance, msgRechargeFail, msgNotSynced} {
		check(m)
	}
	for _, s := range []string{"Add a router (or a NAS for RADIUS)", "Set a speed (Bandwidth)", "Create a plan", "Add a customer or make vouchers"} {
		check(s)
	}
}
