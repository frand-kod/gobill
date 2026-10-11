package web

import (
	"database/sql"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/secret"
)

const isoQRIS = "00020101021126520016ID.CO.CONTOH.WWW01129360000000010212ID10000000015204549953033605802ID5911CONTOH QRIS6006CONTOH9989XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX63048FAE"

// isoReq sends a request from client ip (RemoteAddr) with optional headers as key/value pairs.
func isoReq(h http.Handler, method, path, ip string, form url.Values, hdr ...string) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.RemoteAddr = ip + ":40000"
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func isoSetup(t *testing.T) (*Server, http.Handler, *db.Queries) {
	t.Helper()
	s, q := newTestApp(t)
	s.SecretKey = make([]byte, 32)
	s.Billing = &billing.Service{DB: s.conn, Q: q, Key: s.SecretKey, Loc: time.UTC}
	return s, s.Handler(), q
}

func isoSettings(t *testing.T, q *db.Queries, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
}

// isoCustomer makes customer budi (password pw) whose last plan (150000) expired yesterday, with an
// open RADIUS session on ip. isolated adds the active isolir subscription.
func isoCustomer(t *testing.T, s *Server, q *db.Queries, ip string, isolated bool) db.Customer {
	t.Helper()
	ctx, now := t.Context(), time.Now()
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b10", RateDown: 10, RateDownUnit: "Mbps", RateUp: 5, RateUpUnit: "Mbps"})
	plan, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "Paket 10M", Type: "Hotspot", Billing: "prepaid", Price: 150000,
		Validity: 30, ValidityUnit: "Days", Enabled: 1, Device: "Radius", BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := secret.Seal(s.SecretKey, []byte("pw"))
	c, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "budi", PasswordHash: "h", Fullname: "Budi Santoso", ServiceType: "Hotspot", SecretEnc: sealed, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: plan.ID, Type: "Hotspot",
		StartedAt: now.AddDate(0, 0, -31).Unix(), ExpiresAt: now.AddDate(0, 0, -1).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(ctx, "UPDATE subscriptions SET status = 'expired' WHERE id = ?", old.ID); err != nil {
		t.Fatal(err)
	}
	if isolated {
		isoID, err := isolir.EnsurePlan(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: isoID, Type: "Hotspot",
			StartedAt: now.Unix(), ExpiresAt: now.AddDate(0, 0, 30).Unix(), Method: "Isolir"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.conn.ExecContext(ctx, `INSERT INTO radius_sessions (session_id, username, nas_ip, framed_ip, started_at, updated_at)
		VALUES ('s1', 'budi', '10.0.0.1', ?, ?, ?)`, ip, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIsolirPageKnownCustomer(t *testing.T) {
	s, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"qris_payload": isoQRIS, "isolir_wa": "081234567890", "company_name": "ISP Test"})
	isoCustomer(t, s, q, "203.0.113.7", true)
	w := isoReq(h, "GET", "/isolir", "203.0.113.7", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Halo <b>Budi Santoso</b>", "Paket 10M", money(150000), "data:image/png;base64,", "Simpan gambar",
		"https://wa.me/6281234567890?text=", "Paket internet Anda sudah habis", `action="/isolir/voucher"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if w.Header().Get("Content-Security-Policy") != "" {
		t.Error("default layout must not be sandboxed")
	}
}

func TestIsolirPageUnknownIsGeneric(t *testing.T) {
	s, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"qris_payload": isoQRIS, "isolir_wa": "081234567890"})
	isoCustomer(t, s, q, "203.0.113.7", true)
	w := isoReq(h, "GET", "/isolir", "198.51.100.9", nil)
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Contains(body, "Halo <b>") || strings.Contains(body, "data:image/png") || strings.Contains(body, "Budi") {
		t.Fatalf("unknown client leaked a customer: %d", w.Code)
	}
	if !strings.Contains(body, "Paket internet Anda sudah habis") {
		t.Error("generic page misses the title")
	}
}

func TestIsolirPageExemptFromMaintenance(t *testing.T) {
	_, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"maintenance_mode": "yes"})
	if w := isoReq(h, "GET", "/isolir", "198.51.100.9", nil); w.Code != http.StatusOK {
		t.Fatalf("maintenance blocked /isolir: %d", w.Code)
	}
}

func TestIsolirCustomModeSandboxedAndVariables(t *testing.T) {
	s, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"isolir_mode": "custom",
		"isolir_html": `<h1>Hi [[name]]</h1><p>[[plan_name]] [[known]] [[nope]]</p>[[voucher_form]]<script>var a=1</script>`})
	isoCustomer(t, s, q, "203.0.113.7", true)
	w := isoReq(h, "GET", "/isolir", "203.0.113.7", nil)
	if got := w.Header().Get("Content-Security-Policy"); got != isolirCSP || strings.Contains(got, "allow-same-origin") {
		t.Fatalf("CSP %q", got)
	}
	body := w.Body.String()
	for _, want := range []string{"<h1>Hi Budi Santoso</h1>", "Paket 10M yes", `action="/isolir/voucher"`, "<script>var a=1</script>"} {
		if !strings.Contains(body, want) {
			t.Errorf("custom page misses %q in %s", want, body)
		}
	}
	if strings.Contains(body, "[[") {
		t.Error("unknown variable left in the page")
	}
}

func TestIsolirCustomBlankFallsBackToDefault(t *testing.T) {
	_, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"isolir_mode": "custom", "isolir_html": " \n\t "})
	w := isoReq(h, "GET", "/isolir", "198.51.100.9", nil)
	if w.Header().Get("Content-Security-Policy") != "" || !strings.Contains(w.Body.String(), "Paket internet Anda sudah habis") {
		t.Fatal("blank custom HTML must fall back to the default page")
	}
}

func TestRenderIsolirCustomEscapesValues(t *testing.T) {
	out := renderIsolirCustom(`[[name]]|[[promos]]|[[qris_image]]|[[nope]]|[[voucher_form]]`, isolirView{
		Name: `<a>"x"</a>`, Promos: template.HTML("<b>p</b>"), QRIS: template.URL("data:image/png;base64,AA"), VoucherForm: "<form>v</form>"})
	want := `&lt;a&gt;&#34;x&#34;&lt;/a&gt;|<b>p</b>|data:image/png;base64,AA||<form>v</form>`
	if out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
}

func TestIsolirVoucherWithoutSessionAndRateLimited(t *testing.T) {
	s, h, q := isoSetup(t)
	cross := []string{"Origin", "null", "Sec-Fetch-Site", "cross-site"} // the sandboxed page posts like this
	w := isoReq(h, "POST", "/isolir/voucher", "198.51.100.9", url.Values{"code": {"ABC"}}, cross...)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Masukkan voucher di halaman login hotspot") {
		t.Fatalf("unknown client: %d %s", w.Code, w.Body)
	}
	isoCustomer(t, s, q, "203.0.113.7", true)
	for i := 0; i < maxFailedLogins; i++ {
		w = isoReq(h, "POST", "/isolir/voucher", "203.0.113.7", url.Values{"code": {"NOPE"}}, cross...)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Voucher tidak valid") {
			t.Fatalf("try %d: %d %s", i, w.Code, w.Body)
		}
	}
	w = isoReq(h, "POST", "/isolir/voucher", "203.0.113.7", url.Values{"code": {"NOPE"}}, cross...)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "Terlalu banyak percobaan") {
		t.Fatalf("limit: %d", w.Code)
	}
}

func TestIsolirSettingsSuperAdminOnly(t *testing.T) {
	_, h, _ := settingsSetup(t)
	bob := login(t, h, "bob") // Admin
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/settings/isolir"},
		{"POST", "/admin/settings/isolir"},
		{"GET", "/admin/settings/isolir/preview"},
		{"POST", "/admin/settings/isolir/reset/paket"},
		{"POST", "/admin/settings/isolir/reset/html"},
	} {
		if w := do(h, c.method, c.path, url.Values{}, bob); w.Code != http.StatusForbidden {
			t.Errorf("%s %s as Admin: %d, want 403", c.method, c.path, w.Code)
		}
	}
	alice := login(t, h, "alice")
	if w := do(h, "GET", "/admin/settings/isolir", nil, alice); w.Code != http.StatusOK {
		t.Fatalf("SuperAdmin tab: %d", w.Code)
	}
}

func TestIsolirSettingsDefaultsShownAndSaveCreatesPlan(t *testing.T) {
	_, h, q := settingsSetup(t)
	alice := login(t, h, "alice")
	body := getBody(t, h, alice, "/admin/settings/isolir")
	for _, want := range []string{`name="isolir_days" type="number" value="30"`, `name="isolir_title" type="text" value="Paket internet Anda sudah habis"`,
		`name="isolir_address_list" type="text" value="isolir"`} {
		if !strings.Contains(body, want) {
			t.Errorf("default not shown: %s", want)
		}
	}
	form := url.Values{"isolir_enabled": {"yes"}, "isolir_plan_id": {""}, "isolir_days": {"30"}, "isolir_title": {"T"},
		"isolir_message": {"M"}, "isolir_wa": {""}, "isolir_show_qris": {"yes"}, "isolir_show_voucher": {"yes"},
		"isolir_address_list": {"isolir"}, "isolir_mode": {"default"}, "isolir_html": {""}}
	if w := do(h, "POST", "/admin/settings/isolir", form, alice); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	id := settingValues(t, q)["isolir_plan_id"]
	if id == "" {
		t.Fatal("isolir plan not stored")
	}
	if w := do(h, "POST", "/admin/settings/isolir", form, alice); w.Code != http.StatusSeeOther {
		t.Fatalf("second save: %d", w.Code)
	}
	if again := settingValues(t, q)["isolir_plan_id"]; again != id {
		t.Fatalf("second save made another plan: %s vs %s", again, id)
	}
	plans, _ := q.ListPlans(t.Context(), db.ListPlansParams{Limit: 100})
	n := 0
	for _, p := range plans {
		if p.Name == isolir.PlanName {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d plans named Isolir", n)
	}
	bad := url.Values{}
	for k, v := range form {
		bad[k] = v
	}
	bad.Set("isolir_days", "-1")
	if w := do(h, "POST", "/admin/settings/isolir", bad, alice); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("negative days: %d", w.Code)
	}
}

func TestIsolirResetDefaults(t *testing.T) {
	_, h, q := settingsSetup(t)
	alice := login(t, h, "alice")
	isoSettings(t, q, map[string]string{"isolir_days": "5", "isolir_enabled": "yes"})
	if w := do(h, "POST", "/admin/settings/isolir/reset/paket", url.Values{}, alice); w.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d", w.Code)
	}
	v := settingValues(t, q)
	if v["isolir_days"] != "30" || v["isolir_enabled"] != "no" {
		t.Fatalf("after reset: days=%s enabled=%s", v["isolir_days"], v["isolir_enabled"])
	}
}

func TestRadiusRestIsolirAcceptCarriesAddressList(t *testing.T) {
	s, h, q := isoSetup(t)
	isoCustomer(t, s, q, "203.0.113.7", true)
	w := post(h, "/radius.php?action=authorize", url.Values{"username": {"budi"}, "password": {"pw"}}, "")
	if w.Code != 200 {
		t.Fatalf("code %d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"reply:Mikrotik-Address-List":"isolir"`) || !strings.Contains(body, `"reply:Mikrotik-Rate-Limit"`) {
		t.Fatalf("isolated accept misses address-list or rate limit: %s", body)
	}
}
