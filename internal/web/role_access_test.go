package web

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// Roles in matrix order: SuperAdmin, Admin, Agent, Sales, Report.
var matrixRoles = []string{"alice", "adm", "agnes", "sally", "rita"}

// strictMatrix: who may call the routes that role_access_mode changes (strict mode).
// M = managers, S = staff (managers + Agent + Sales), A = every admin.
var strictMatrix = []struct{ method, path, who string }{
	{"GET", "/admin/deposit", "M"},
	{"POST", "/admin/deposit", "M"},
	{"GET", "/admin/vouchers/new", "M"},
	{"POST", "/admin/vouchers", "M"},
	{"GET", "/admin/customers/export", "S"},
	{"GET", "/admin/search.json?q=u", "S"},
	{"GET", "/admin/subscriptions/export", "S"},
	{"GET", "/admin/isolations/export", "S"},
	{"GET", "/admin/transactions", "S"},
	{"GET", "/admin/transactions/1/invoice", "S"},
	{"GET", "/admin/reports", "A"},
	{"GET", "/admin/reports/period", "A"},
	{"GET", "/admin/vouchers", "A"},
}

var whoRoles = map[string]string{"M": "alice adm", "S": "alice adm agnes sally", "A": "alice adm agnes sally rita"}

func roleEnv(t *testing.T) (*billEnv, map[string]*http.Cookie) {
	e := billApp(t)
	mk(t, e.q, "adm", "Admin", 1)
	mk(t, e.q, "agnes", "Agent", 1)
	mk(t, e.q, "sally", "Sales", 1)
	ck := map[string]*http.Cookie{}
	for _, u := range matrixRoles {
		ck[u] = login(t, e.h, u)
	}
	return e, ck
}

func TestRoleMatrixStrict(t *testing.T) {
	e, ck := roleEnv(t) // role_access_mode unset = strict
	for _, r := range strictMatrix {
		for _, u := range matrixRoles {
			form := url.Values(nil)
			if r.method == "POST" {
				form = url.Values{}
			}
			code := do(e.h, r.method, r.path, form, ck[u]).Code
			want := strings.Contains(" "+whoRoles[r.who]+" ", " "+u+" ")
			if got := code != 403; got != want {
				t.Errorf("%s %s as %s: status %d, allowed should be %v", r.method, r.path, u, code, want)
			}
			if want && r.method == "GET" && !strings.Contains(r.path, "invoice") && code != 200 {
				t.Errorf("GET %s as %s: %d, want 200", r.path, u, code)
			}
		}
	}
}

// Every route that routes.go wraps in view.Money(...) or noReport(...) must be in strictMatrix.
func TestRoleMatrixCoversRoutes(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, r := range strictMatrix {
		have[r.method+" "+strings.SplitN(r.path, "?", 2)[0]] = true
	}
	re := regexp.MustCompile(`"(GET|POST) (/\S+)", (?:money|noReport)\(`)
	n := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		n++
		if k := m[1] + " " + strings.ReplaceAll(m[2], "{id}", "1"); !have[k] {
			t.Errorf("%s is mode-dependent in routes.go but missing from strictMatrix", k)
		}
	}
	if n < 10 {
		t.Fatalf("found only %d mode-dependent routes, the pattern is stale", n)
	}
}

func TestRoleAccessPHPNuxBillMode(t *testing.T) {
	e, ck := roleEnv(t)
	if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "role_access_mode", Value: "phpnuxbill"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, do(e.h, "GET", "/admin/deposit", nil, ck["agnes"]), 200, "Agent deposit form")
	wantCode(t, do(e.h, "GET", "/admin/vouchers/new", nil, ck["sally"]), 200, "Sales voucher form")
	wantCode(t, do(e.h, "GET", "/admin/transactions", nil, ck["rita"]), 200, "Report transactions")
	wantCode(t, do(e.h, "GET", "/admin/customers/export", nil, ck["rita"]), 200, "Report export")
}

func TestRoleAccessRechargeZeroStrict(t *testing.T) {
	e, ck := roleEnv(t)
	p := e.plan(t, "day", "PPPoE", 10000)
	path := "/admin/customers/" + itoa(e.cust.ID) + "/recharge"
	do(e.h, "POST", path, url.Values{"plan": {itoa(p.ID)}, "method": {"Zero"}}, ck["agnes"])
	var n int
	e.s.conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n)
	if n != 0 {
		t.Fatalf("Agent recharged for free in strict mode: %d transactions", n)
	}
	do(e.h, "POST", path, url.Values{"plan": {itoa(p.ID)}, "method": {"Zero"}}, ck["adm"])
	e.s.conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n)
	if n != 1 {
		t.Fatalf("Admin Recharge Zero: %d transactions", n)
	}
}
