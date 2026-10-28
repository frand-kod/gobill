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

const isoQRIS = "00020101021126520016ID.CO.CONTOH.WWW01129360000000010212ID10000000015204549953033605802ID5911CONTOH QRIS6006CONTOH9954XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX63048687"

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
		if _, err := isolir.EnsurePlan(ctx, q); err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: c.ID, StartedAt: now.Unix(), EndsAt: now.AddDate(0, 0, 30).Unix()}); err != nil {
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
	for _, want := range []string{"Halo <b>Budi Santoso</b>", "Paket 10M", "Hubungi admin",
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
	out := renderIsolirCustom(`[[name]]|[[promos]]|[[qris_image]]|[[qris_merchant]]|[[nope]]|[[voucher_form]]`, isolirView{
		Name: `<a>"x"</a>`, Promos: template.HTML("<b>p</b>"), VoucherForm: "<form>v</form>"})
	// old QRIS variables are no longer filled: they become empty so old custom pages still render
	want := `&lt;a&gt;&#34;x&#34;&lt;/a&gt;|<b>p</b>||||<form>v</form>`
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
	isoEnable(t, s, q)
	w := post(h, "/radius.php?action=authorize", url.Values{"username": {"budi"}, "password": {"pw"}}, "")
	if w.Code != 200 {
		t.Fatalf("code %d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"reply:Mikrotik-Address-List":"isolir"`) || !strings.Contains(body, `"reply:Mikrotik-Rate-Limit"`) {
		t.Fatalf("isolated accept misses address-list or rate limit: %s", body)
	}
}

// isoEnable turns isolir on and reloads the cached flag, as the settings save does in production.
func isoEnable(t *testing.T, s *Server, q *db.Queries) {
	t.Helper()
	isoSettings(t, q, map[string]string{"isolir_enabled": "yes"})
	s.ReloadSessionSettings(t.Context())
}

func TestIsolirGateServesPageForAnyHostAndPath(t *testing.T) {
	s, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"qris_payload": isoQRIS, "isolir_wa": "081234567890"})
	isoCustomer(t, s, q, "203.0.113.7", true)
	isoEnable(t, s, q)
	want := isoReq(h, "GET", "/isolir", "203.0.113.7", nil).Body.String()
	for _, path := range []string{"/", "/random/path", "http://example.com/some/path?x=1"} {
		w := isoReq(h, "GET", path, "203.0.113.7", nil)
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("GET %s: code %d, want the isolir page", path, w.Code)
		}
	}
	if w := isoReq(h, "HEAD", "http://example.com/x", "203.0.113.7", nil); w.Code != http.StatusOK {
		t.Errorf("HEAD: code %d", w.Code)
	}
}

func TestIsolirGateLetsIsolatedClientReachPortalAndAssets(t *testing.T) {
	s, h, q := isoSetup(t)
	isoCustomer(t, s, q, "203.0.113.7", true)
	isoEnable(t, s, q)
	want := isoReq(h, "GET", "/isolir", "203.0.113.7", nil).Body.String()
	for _, path := range []string{"/static/app.css", "/login", "/portal", "/health", "/uploads/none.png"} {
		if w := isoReq(h, "GET", path, "203.0.113.7", nil); w.Body.String() == want {
			t.Errorf("GET %s was hijacked", path)
		}
	}
	if w := isoReq(h, "GET", "/login", "203.0.113.7", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<form") {
		t.Errorf("portal login: code %d", w.Code)
	}
}

func TestIsolirGateLeavesOthersAlone(t *testing.T) {
	s, h, q := isoSetup(t)
	isoCustomer(t, s, q, "203.0.113.7", false) // expired, not isolated
	isoEnable(t, s, q)
	if w := isoReq(h, "GET", "/", "203.0.113.7", nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Errorf("non-isolated customer: code %d", w.Code)
	}
	if w := isoReq(h, "GET", "/", "198.51.100.9", nil); w.Code != http.StatusSeeOther {
		t.Errorf("client without session: code %d", w.Code)
	}
}

func TestIsolirGateOffWhenDisabled(t *testing.T) {
	s, h, q := isoSetup(t)
	isoCustomer(t, s, q, "203.0.113.7", true)
	if w := isoReq(h, "GET", "/", "203.0.113.7", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("isolir disabled but root hijacked: %d", w.Code)
	}
	isoEnable(t, s, q)
	isoSettings(t, q, map[string]string{"isolir_enabled": "no"})
	s.ReloadSessionSettings(t.Context())
	if w := isoReq(h, "GET", "/", "203.0.113.7", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("isolir turned off but root hijacked: %d", w.Code)
	}
}

func TestIsolirSetupCommandsUseListenAddress(t *testing.T) {
	s, _, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"app_url": "https://billing.example.com"})
	s.ListenAddr = "192.168.99.2:8090"
	cmds := map[string]string{}
	for _, f := range s.isolirSetupFields(map[string]string{"app_url": "https://billing.example.com"}) {
		cmds[f.Name] = f.Value
	}
	for name, want := range map[string]string{
		"isolir_cmd_rules":  "dst-address-type=!local protocol=tcp dst-port=80 action=dst-nat to-addresses=192.168.99.2 to-ports=8090",
		"isolir_cmd_walled": "dst-address=192.168.99.2 dst-port=8090",
		"isolir_cmd_bypass": "type=bypassed",
		"isolir_cmd_test":   `curl -i -H "Host: example.com" http://192.168.99.2:8090/`,
	} {
		if !strings.Contains(cmds[name], want) {
			t.Errorf("%s = %q, want it to contain %q", name, cmds[name], want)
		}
	}
	s.ListenAddr = ":8090"
	if host, port := s.gobillAddr(); host != lanIPv4() || port != "8090" {
		t.Errorf("wildcard listen gave %s:%s", host, port)
	}
}

// The logout button shows only when a hotspot address is set, and points at its /logout.
func TestIsolirLogoutButton(t *testing.T) {
	s, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"qris_payload": isoQRIS})
	isoCustomer(t, s, q, "203.0.113.7", true)
	if body := isoReq(h, "GET", "/isolir", "203.0.113.7", nil).Body.String(); strings.Contains(body, "/logout") {
		t.Fatal("logout button shown without a hotspot address")
	}
	isoSettings(t, q, map[string]string{"isolir_hotspot_url": "http://192.168.20.1/"})
	body := isoReq(h, "GET", "/isolir", "203.0.113.7", nil).Body.String()
	if !strings.Contains(body, `href="http://192.168.20.1/logout"`) || !strings.Contains(body, "Logout / pakai akun lain") {
		t.Fatal("logout button missing or wrong link")
	}
}

// [[logout_url]] is the logout address in custom HTML, empty when unset, and it is escaped.
func TestIsolirLogoutURLVariable(t *testing.T) {
	if got := renderIsolirCustom("[[logout_url]]", isolirView{}); got != "" {
		t.Fatalf("unset: %q", got)
	}
	v := isolirView{LogoutURL: isoLogout("http://fourkeys.net")}
	if got := renderIsolirCustom(`<a href="[[logout_url]]">x</a>`, v); got != `<a href="http://fourkeys.net/logout">x</a>` {
		t.Fatalf("set: %q", got)
	}
	if got := isoLogout(`http://x"y`); strings.Contains(renderIsolirCustom("[[logout_url]]", isolirView{LogoutURL: got}), `"`) {
		t.Fatal("quote not escaped")
	}
}

// isoLogout is the logout address for a hotspot address, as the page builds it.
func isoLogout(base string) string {
	return isolirLogoutURL(map[string]string{"isolir_hotspot_url": base + "/"})
}

// The settings page takes only http or https hotspot addresses.
func TestIsolirHotspotURLValidated(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	for _, bad := range []string{"javascript:alert(1)", "fourkeys.net", "ftp://192.168.20.1"} {
		if w := do(h, "POST", "/admin/settings/isolir", url.Values{"isolir_hotspot_url": {bad}, "isolir_mode": {"default"}}, c); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q accepted: %d", bad, w.Code)
		}
	}
}

// The preview has no QR: the isolir page never offers payment. It shows the contact and logout buttons.
func TestIsolirPreviewContactAdmin(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	isoSettings(t, q, map[string]string{"isolir_wa": "081234567890", "isolir_hotspot_url": "http://192.168.20.1"})
	body := do(h, "GET", "/admin/settings/isolir/preview", nil, c).Body.String()
	for _, want := range []string{"Contact admin", "https://wa.me/6281234567890", `href="http://192.168.20.1/logout"`} {
		if !strings.Contains(body, want) {
			t.Errorf("preview misses %q", want)
		}
	}
	if strings.Contains(body, "data:image/png") {
		t.Error("preview shows a QR code")
	}
}

// The router block is one paste: the NAT and the gobill accept, exact hosts in the allow address-list,
// a tls-host rule for every allow entry, then the SYN, allowed-apps and drop rules in that order.
func TestIsolirRouterRulesBlock(t *testing.T) {
	s, _, _ := isoSetup(t)
	s.ListenAddr = "192.168.99.2:8090"
	block := func(st map[string]string) string {
		for _, f := range s.isolirSetupFields(st) {
			if f.Name == "isolir_cmd_rules" {
				return f.Value
			}
		}
		t.Fatal("no isolir_cmd_rules field")
		return ""
	}
	b := block(map[string]string{"isolir_address_list": "fw-iso", "isolir_allow_hosts": "*.whatsapp.net\n\n  wa.me \n Bank.Example \n"})
	for _, want := range []string{
		"/ip firewall nat add chain=dstnat src-address-list=fw-iso dst-address-type=!local protocol=tcp dst-port=80 action=dst-nat to-addresses=192.168.99.2 to-ports=8090",
		"/ip firewall filter add chain=forward src-address-list=fw-iso dst-address=192.168.99.2 action=accept",
		`/ip firewall address-list add list=fw-iso-allow address=wa.me comment="gobill isolir allow"`,
		`/ip firewall address-list add list=fw-iso-allow address=bank.example comment="gobill isolir allow"`,
		"tls-host=*.whatsapp.net action=add-dst-to-address-list address-list=fw-iso-allow address-list-timeout=1d",
		"tls-host=wa.me ", "tls-host=bank.example ",
		"tcp-flags=syn action=accept",
		"dst-address-list=fw-iso-allow action=accept",
		"src-address-list=fw-iso action=drop",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("block misses %q", want)
		}
	}
	if strings.Contains(b, "address=*.whatsapp.net") || strings.Contains(b, "src-address-list=isolir ") {
		t.Errorf("wildcard in the address-list or the default list name:\n%s", b)
	}
	order := []string{"tls-host=wa.me", "tcp-flags=syn", "dst-address-list=fw-iso-allow", "action=drop"}
	last := -1
	for _, o := range order {
		i := strings.Index(b, o)
		if i <= last {
			t.Fatalf("%q out of order in:\n%s", o, b)
		}
		last = i
	}
	if !strings.HasSuffix(b, "action=drop comment=\"isolir: block the rest\"") {
		t.Errorf("drop is not the last rule:\n%s", b)
	}
	// unset: the default list and the default allow-list (WhatsApp)
	d := block(nil)
	for _, want := range []string{"src-address-list=isolir dst-address-type", "list=isolir-allow address=wa.me", "tls-host=*.whatsapp.net", "tls-host=g.whatsapp.net"} {
		if !strings.Contains(d, want) {
			t.Errorf("default block misses %q", want)
		}
	}
}

// Allow-list lines must be hostnames or *. wildcards; blank lines are fine.
func TestIsolirAllowHostsValidated(t *testing.T) {
	s, _, _ := isoSetup(t)
	for _, bad := range []string{"foo bar", "http://x", "*bad", "*.", "a..b"} {
		if e := s.settingsErrors(map[string]string{"isolir_allow_hosts": "wa.me\n" + bad}); e["isolir_allow_hosts"] == "" {
			t.Errorf("%q accepted", bad)
		}
	}
	if e := s.settingsErrors(map[string]string{"isolir_allow_hosts": "wa.me\r\n\r\n*.whatsapp.net\r\n"}); e["isolir_allow_hosts"] != "" {
		t.Errorf("valid list rejected: %v", e)
	}
}
