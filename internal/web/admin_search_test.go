package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// searchURLs returns every result URL of the palette for q, with the cookie's role.
func searchURLs(t *testing.T, h http.Handler, c *http.Cookie, q string) []string {
	t.Helper()
	w := do(h, "GET", "/admin/search.json?q="+url.QueryEscape(q), nil, c)
	wantCode(t, w, 200, "search.json "+q)
	var out struct {
		Groups []searchGroup `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v: %s", err, w.Body)
	}
	var urls []string
	for _, g := range out.Groups {
		for _, it := range g.Items {
			urls = append(urls, it.URL)
		}
	}
	return urls
}

func hasURL(urls []string, want string) bool {
	for _, u := range urls {
		if u == want {
			return true
		}
	}
	return false
}

// seedSearch makes one customer with a subscription, an invoice, a voucher, a plan, a router and a NAS.
func seedSearch(t *testing.T, q *db.Queries) (cust db.Customer, plan db.Plan, trx db.Transaction) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().Unix()
	cust = newCust(t, q, "budi01", "Budi Santoso", "081234567", "pp_budi", "Active")
	rt, err := q.CreateRouter(ctx, db.CreateRouterParams{Name: "edge1", Host: "192.168.88.1", Port: 8728, Username: "u", PasswordEnc: []byte("x"), Enabled: 1})
	if err != nil {
		t.Fatal(err)
	}
	bw, err := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = q.CreatePlan(ctx, db.CreatePlanParams{Name: "Paket 10 Mbps", Type: "PPPoE", Billing: "prepaid", Validity: 30, ValidityUnit: "Days",
		Device: "Dummy", Enabled: 1, BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, RouterID: sql.NullInt64{Int64: rt.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: cust.ID, PlanID: plan.ID, Type: "PPPoE",
		StartedAt: now, ExpiresAt: now + 3600, Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	trx, err = q.CreateTransaction(ctx, db.CreateTransactionParams{Invoice: "INV-2610-000123", CustomerID: sql.NullInt64{Int64: cust.ID, Valid: true},
		PlanID: sql.NullInt64{Int64: plan.ID, Valid: true}, Username: cust.Username, PlanName: plan.Name, RouterName: rt.Name, Type: "PPPoE",
		Price: 100000, Method: "Cash"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateVoucher(ctx, db.CreateVoucherParams{Code: "ABC123XYZ", PlanID: plan.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateNAS(ctx, db.CreateNASParams{Name: "nas-kantor", Ip: "10.9.8.7", SecretEnc: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	return cust, plan, trx
}

func TestSearchJSONFindsRecordsByRole(t *testing.T) {
	_, h, q, staff := crudApp(t)
	cust, plan, trx := seedSearch(t, q)
	report := login(t, h, "rita") // Report role: no vouchers, plans, routers or NAS

	custURL := fmt.Sprint("/admin/customers/", cust.ID)
	for _, term := range []string{"budi01", "BUDI", "santoso", "081234", "pp_budi"} {
		if u := searchURLs(t, h, staff, term); !hasURL(u, custURL) {
			t.Errorf("customer by %q: %v", term, u)
		}
	}
	if u := searchURLs(t, h, staff, "10 mbps"); !hasURL(u, fmt.Sprint("/admin/plans/", plan.ID, "/edit")) {
		t.Errorf("plan by name: %v", u)
	}
	if u := searchURLs(t, h, staff, "INV-2610-000123"); !hasURL(u, fmt.Sprint("/admin/transactions/", trx.ID, "/invoice")) {
		t.Errorf("invoice by number: %v", u)
	}
	if u := searchURLs(t, h, staff, "INV-2610"); !hasURL(u, fmt.Sprint("/admin/transactions/", trx.ID, "/invoice")) {
		t.Errorf("invoice by prefix: %v", u)
	}
	if u := searchURLs(t, h, staff, "abc123"); !hasURL(u, "/admin/vouchers?q=ABC123XYZ") {
		t.Errorf("voucher by code for staff: %v", u)
	}
	if u := searchURLs(t, h, staff, "192.168.88"); !hasURL(u, "/admin/routers/1/edit") {
		t.Errorf("router by host: %v", u)
	}
	if u := searchURLs(t, h, staff, "10.9.8"); !hasURL(u, "/admin/nas/1/edit") {
		t.Errorf("NAS by IP: %v", u)
	}

	// Report: customers, subscriptions and invoices only
	if u := searchURLs(t, h, report, "abc123"); len(u) != 0 {
		t.Errorf("voucher for Report: %v", u)
	}
	if u := searchURLs(t, h, report, "10 mbps"); hasURL(u, fmt.Sprint("/admin/plans/", plan.ID, "/edit")) {
		t.Errorf("plan for Report: %v", u)
	}
	if u := searchURLs(t, h, report, "budi01"); !hasURL(u, custURL) {
		t.Errorf("customer for Report: %v", u)
	}
	if u := searchURLs(t, h, report, "Add plan"); hasURL(u, "/admin/plans/new") {
		t.Errorf("quick action Add plan for Report: %v", u)
	}
	if u := searchURLs(t, h, report, "service plan"); hasURL(u, "/admin/plans") {
		t.Errorf("plans page for Report: %v", u)
	}
}

func TestSearchWildcardsAreLiteralAndQueryTruncated(t *testing.T) {
	_, h, q, staff := crudApp(t)
	seedSearch(t, q)
	// % is a plain character: nothing matches it. _ is literal too: it finds the PPPoE name pp_budi only
	for _, term := range []string{"%", "bu%", "b_dget", "%%", "' OR 1=1 --"} {
		for _, u := range searchURLs(t, h, staff, term) {
			if strings.HasPrefix(u, "/admin/customers/") || strings.HasPrefix(u, "/admin/vouchers") || strings.HasPrefix(u, "/admin/transactions/") {
				t.Errorf("%q matched record %s", term, u)
			}
		}
	}
	if u := searchURLs(t, h, staff, "_"); !hasURL(u, "/admin/customers/1") {
		t.Errorf("_ must match the literal underscore in pp_budi: %v", u)
	}
	if got := searchQuery(strings.Repeat("a", 100)); len([]rune(got)) != searchMaxQ {
		t.Errorf("query not cut to %d: %d", searchMaxQ, len([]rune(got)))
	}
	// a long query still answers (cut to 64 characters, so the match is on the first 64 only)
	if u := searchURLs(t, h, staff, "budi01"+strings.Repeat("z", 80)); len(u) != 0 {
		t.Errorf("long query matched %v", u)
	}
}

func TestSearchSettingsEntries(t *testing.T) {
	_, h, q, superAdmin := crudApp(t) // alice: SuperAdmin, sees every tab
	mk(t, q, "ada", "Admin", 1)
	adminCookie := login(t, h, "ada")
	report := login(t, h, "rita")

	want := map[string]string{
		"qris":            "/admin/settings/payment#f-qris_payload",
		"timeout":         "/admin/settings/miscellaneous#f-session_timeout_duration",
		"batas waktu":     "/admin/settings/miscellaneous#f-session_timeout_duration",
		"session_timeout": "/admin/settings/miscellaneous#f-session_timeout_duration",
		"gowa":            "/admin/settings/integrations#f-alt_wga_server_url",
		"pengingat":       "/admin/settings/notifications#f-reminder_hour",
		"reminder":        "/admin/settings/notifications#f-reminder_hour",
	}
	for term, u := range want {
		if got := searchURLs(t, h, superAdmin, term); !hasURL(got, u) {
			t.Errorf("%q: want %s in %v", term, u, got)
		}
	}

	// Integrations and Payment are SuperAdmin only; Admin keeps the other tabs, Report gets none
	if got := searchURLs(t, h, adminCookie, "qris"); hasURL(got, "/admin/settings/payment#f-qris_payload") {
		t.Errorf("Admin sees the payment tab: %v", got)
	}
	if got := searchURLs(t, h, adminCookie, "timeout"); !hasURL(got, "/admin/settings/miscellaneous#f-session_timeout_duration") {
		t.Errorf("Admin misses the miscellaneous tab: %v", got)
	}
	if got := searchURLs(t, h, report, "timeout"); hasURL(got, "/admin/settings/miscellaneous#f-session_timeout_duration") {
		t.Errorf("Report sees settings: %v", got)
	}
}

func TestSearchFallbackPageRendersGroups(t *testing.T) {
	_, h, q, c := crudApp(t)
	cust, _, _ := seedSearch(t, q)
	w := do(h, "GET", "/admin/search?q=budi01", nil, c)
	wantCode(t, w, 200, "fallback page")
	b := w.Body.String()
	for _, want := range []string{`<h2 class="card-title">`, `href="/admin/customers/` + fmt.Sprint(cust.ID) + `"`, "budi01", `action="/admin/search"`} {
		if !strings.Contains(b, want) {
			t.Errorf("fallback page lacks %q", want)
		}
	}
	// the plain page also works for the Report role, with the groups it may open
	wantCode(t, do(h, "GET", "/admin/search?q=abc123", nil, login(t, h, "rita")), 200, "report fallback")
	wantCode(t, do(h, "GET", "/admin/search?q=x", nil, nil), 303, "anonymous fallback")
}
