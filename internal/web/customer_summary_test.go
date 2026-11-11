package web

import (
	"context"
	"encoding/json"
	"github.com/frand-kod/gobill/internal/web/view"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// summaryOf fetches /admin/customers/{id}/summary and decodes it as generic JSON.
func summaryOf(t *testing.T, h http.Handler, c *http.Cookie, target string) (map[string]any, string) {
	t.Helper()
	w := do(h, "GET", target, nil, c)
	wantCode(t, w, 200, target)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: json: %v: %s", target, err, w.Body)
	}
	return out, w.Body.String()
}

func subsOf(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	raw, _ := m["subscriptions"].([]any)
	var out []map[string]any
	for _, x := range raw {
		out = append(out, x.(map[string]any))
	}
	return out
}

// The summary carries status, balance, active plans and the last transactions, and the effect a
// recharge of the chosen plan would have (same plan extends, another plan on the same router replaces).
func TestCustomerSummaryFields(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	home := e.plan(t, "Home", "PPPoE", 100000)
	other := e.plan(t, "Other", "PPPoE", 150000)
	cu, err := e.q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "sumcust", PasswordHash: "h", Fullname: "Sum Cust",
		Phone: "0811SECRETPHONE", Email: "secret@example.test", Address: "JalanRahasia 7", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.Billing.Recharge(ctx, cu.ID, home.ID, "Cash - Alice", 0); err != nil {
		t.Fatal(err)
	}

	s1, body := summaryOf(t, e.h, e.c, "/admin/customers/"+itoa(cu.ID)+"/summary?plan="+itoa(home.ID))
	if s1["username"] != "sumcust" || s1["fullname"] != "Sum Cust" || s1["status"] != "Active" || s1["blocks_recharge"] != false {
		t.Errorf("identity/status: %+v", s1)
	}
	if s1["balance"] != view.Money(0) || s1["voucher"] != nil {
		t.Errorf("balance/voucher: %+v", s1)
	}
	subs := subsOf(t, s1)
	if len(subs) != 1 || subs[0]["plan"] != "Home" || subs[0]["effect"] != "extend" {
		t.Fatalf("same plan should extend: %+v", subs)
	}
	if d := subs[0]["days_left"].(float64); d < 0 || d > 2 || subs[0]["expires_at"] == "" {
		t.Errorf("expiry fields: %+v", subs[0])
	}
	trx := s1["transactions"].([]any)
	if len(trx) != 1 {
		t.Fatalf("transactions: %+v", trx)
	}
	if tr := trx[0].(map[string]any); tr["plan"] != "Home" || tr["price"] != view.Money(100000) || tr["method"] != "Cash - Alice" || tr["invoice"] == "" || tr["date"] == "" {
		t.Errorf("transaction row: %+v", tr)
	}

	// another plan on the same router and type replaces the active one
	s2, _ := summaryOf(t, e.h, e.c, "/admin/customers/"+itoa(cu.ID)+"/summary?plan="+itoa(other.ID))
	if subs := subsOf(t, s2); len(subs) != 1 || subs[0]["effect"] != "replace" {
		t.Errorf("other plan should replace: %+v", subs)
	}
	// extend_expiry=no: the same plan replaces too (billing then starts a new period)
	setting(t, e, "extend_expiry", "no")
	if subs := subsOf(t, mustSummary(t, e, cu.ID, home.ID)); subs[0]["effect"] != "replace" {
		t.Errorf("extend_expiry=no: %+v", subs)
	}
	setting(t, e, "extend_expiry", "yes")
	// no plan chosen: no effect is claimed
	if subs := subsOf(t, mustSummary(t, e, cu.ID, 0)); len(subs) != 1 || subs[0]["effect"] != "" {
		t.Errorf("no plan: %+v", subs)
	}
	// a balance top-up is not a subscription, so it has no effect either
	bal := e.plan(t, "Topup", "Balance", 0)
	if subs := subsOf(t, mustSummary(t, e, cu.ID, bal.ID)); subs[0]["effect"] != "" {
		t.Errorf("balance plan: %+v", subs)
	}

	// privacy: no contact, address, coordinates or secret fields and no values of them
	for _, bad := range []string{"phone", "email", "address", "coordinates", "password", "secret", "pppoe"} {
		if strings.Contains(body, `"`+bad) {
			t.Errorf("summary leaks %q key: %s", bad, body)
		}
	}
	for _, v := range []string{"0811SECRETPHONE", "secret@example.test", "JalanRahasia", "$2a$"} {
		if strings.Contains(body, v) {
			t.Errorf("summary leaks %q", v)
		}
	}
}

func mustSummary(t *testing.T, e *billEnv, id, plan int64) map[string]any {
	t.Helper()
	m, _ := summaryOf(t, e.h, e.c, "/admin/customers/"+itoa(id)+"/summary?plan="+itoa(plan))
	return m
}

// An inactive account is flagged: recharge is refused for it.
func TestCustomerSummaryInactive(t *testing.T) {
	e := billApp(t)
	cu := newCust(t, e.q, "offline1", "Off Line", "", "", "Disabled")
	m, _ := summaryOf(t, e.h, e.c, "/admin/customers/"+itoa(cu.ID)+"/summary")
	if m["blocks_recharge"] != true || m["status"] != "Disabled" {
		t.Errorf("disabled account must block recharge: %+v", m)
	}
	if subs := subsOf(t, m); len(subs) != 0 {
		t.Errorf("no subscriptions expected: %+v", subs)
	}
}

// A customer who redeemed a voucher is a voucher account: labelled with the code and plan, no personal name.
func TestCustomerSummaryVoucherAccount(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	plan := e.plan(t, "Voucher Plan", "PPPoE", 50000)
	if _, err := e.q.CreateVoucher(ctx, db.CreateVoucherParams{Code: "VOUCH1234", PlanID: plan.ID}); err != nil {
		t.Fatal(err)
	}
	vc := newCust(t, e.q, "08123456789", "Real Name", "08123456789", "", "Active")
	if err := e.s.Billing.RedeemVoucher(ctx, "VOUCH1234", vc.ID); err != nil {
		t.Fatal(err)
	}
	m, body := summaryOf(t, e.h, e.c, "/admin/customers/"+itoa(vc.ID)+"/summary")
	v, ok := m["voucher"].(map[string]any)
	if !ok || v["code"] != "VOUCH1234" || v["plan"] != "Voucher Plan" {
		t.Fatalf("voucher account: %+v", m["voucher"])
	}
	if m["fullname"] != "" || strings.Contains(body, "Real Name") {
		t.Errorf("voucher account must not show the personal name: %s", body)
	}
	if subs := subsOf(t, m); len(subs) != 1 || subs[0]["plan"] != "Voucher Plan" {
		t.Errorf("voucher plan should be active: %+v", subs)
	}

	// a plain customer has no voucher
	plain := newCust(t, e.q, "plain1", "Plain", "", "", "Active")
	if m, _ := summaryOf(t, e.h, e.c, "/admin/customers/"+itoa(plain.ID)+"/summary"); m["voucher"] != nil || m["fullname"] != "Plain" {
		t.Errorf("plain customer: %+v", m)
	}
}

// Same staff roles as the recharge page: Report is refused, anonymous is sent to login, Sales may read.
func TestCustomerSummaryRoles(t *testing.T) {
	e := billApp(t)
	cu := newCust(t, e.q, "rolecust", "Role Cust", "", "", "Active")
	target := "/admin/customers/" + itoa(cu.ID) + "/summary"
	wantCode(t, do(e.h, "GET", target, nil, login(t, e.h, "rita")), 403, "Report role")
	wantCode(t, do(e.h, "GET", target, nil, nil), 303, "anonymous")

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if _, err := e.q.CreateAdmin(context.Background(), db.CreateAdminParams{Username: "sam", Fullname: "Sam", PasswordHash: string(hash), Role: "Sales"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, do(e.h, "GET", target, nil, login(t, e.h, "sam")), 200, "Sales role")
	wantCode(t, do(e.h, "GET", "/admin/customers/999999/summary", nil, e.c), 404, "missing customer")
}

// The recharge page wires the picker event to the summary panel and carries its labels.
func TestRechargeSummaryPanel(t *testing.T) {
	e := billApp(t)
	body := do(e.h, "GET", "/admin/recharge", nil, e.c).Body.String()
	for _, want := range []string{
		`x-data="gbCustomerSummary(`,
		`@customer-picked.window="pick($event.detail.id)"`,
		`@plan-picked.window="setPlan($event.detail)"`,
		`/static/customer-summary.js`,
		`Pilih pelanggan untuk melihat ringkasan`, // the default language is Indonesian
	} {
		if !strings.Contains(body, want) {
			t.Errorf("recharge page: missing %q", want)
		}
	}
}

// The recharge panel shows each active plan's usage from the subscription counters: data of the limit,
// and "Quota used up" with quota_out once the limit is reached.
func TestCustomerSummaryUsage(t *testing.T) {
	e := englishApp(t)
	now := time.Now().Unix()
	dashPlan(t, e, "Data_Limit", 0, 100, now+86400)
	dashSess(t, e, "u1", "a", "10.1.0.1", "", now-3600, now-60, 20<<20, 20<<20) // 40 of 100 MB
	target := "/admin/customers/" + itoa(e.cust.ID) + "/summary"
	subs := subsOf(t, func() map[string]any { m, _ := summaryOf(t, e.h, e.c, target); return m }())
	if len(subs) != 1 || subs[0]["usage"] != "Data used: 40.00 MB of 100.00 MB" || subs[0]["quota_out"] != false {
		t.Fatalf("40 MB: %+v", subs)
	}
	dashSess(t, e, "u1", "b", "10.1.0.1", "", now-1800, now-30, 40<<20, 40<<20) // now 120 MB
	subs = subsOf(t, func() map[string]any { m, _ := summaryOf(t, e.h, e.c, target); return m }())
	if u, _ := subs[0]["usage"].(string); !strings.HasPrefix(u, "Quota used up · Data used: 120.00 MB") || subs[0]["quota_out"] != true {
		t.Fatalf("120 MB: %+v", subs)
	}
}

// A time-only limit still shows the data used; a non-RADIUS plan shows no usage line, since its
// counters are never filled and would read 0 B.
func TestCustomerSummaryUsageTimeAndMikrotik(t *testing.T) {
	e := englishApp(t)
	now := time.Now().Unix()
	dashPlan(t, e, "Time_Limit", 30, 0, now+86400)
	dashSess(t, e, "u1", "a", "10.1.0.1", "", now-3600, now, 1<<20, 1<<20)
	target := "/admin/customers/" + itoa(e.cust.ID) + "/summary"
	get := func() []map[string]any { m, _ := summaryOf(t, e.h, e.c, target); return subsOf(t, m) }
	if u, _ := get()[0]["usage"].(string); !strings.HasPrefix(u, "Time used: ") || !strings.HasSuffix(u, " · Data: 2.00 MB") {
		t.Fatalf("time only: %q", u)
	}

	e2 := englishApp(t)
	home := e2.plan(t, "Home", "PPPoE", 1000) // a MikroTik plan, not RADIUS
	if err := e2.s.Billing.Recharge(t.Context(), e2.cust.ID, home.ID, "Cash", 0); err != nil {
		t.Fatal(err)
	}
	m, _ := summaryOf(t, e2.h, e2.c, "/admin/customers/"+itoa(e2.cust.ID)+"/summary")
	if subs := subsOf(t, m); len(subs) != 1 || subs[0]["usage"] != "" {
		t.Fatalf("mikrotik plan shows usage: %+v", subs)
	}
}
