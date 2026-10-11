package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
)

// Admin never sees the wizard: 403 on every page, and no dialog, sidebar item or link.
func TestSetupAdminGetsNothing(t *testing.T) {
	s, q := newTestApp(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if _, err := q.CreateAdmin(t.Context(), db.CreateAdminParams{Username: "ada", Fullname: "Ada", PasswordHash: string(hash), Role: "Admin"}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	c := login(t, h, "ada")
	for _, p := range []string{"/admin/setup", "/admin/setup/company", "/admin/setup/live.json", "/admin/setup/finish"} {
		if w := do(h, "GET", p, nil, c); w.Code != http.StatusForbidden {
			t.Errorf("GET %s: got %d, want 403", p, w.Code)
		}
	}
	for _, p := range []string{"/admin/setup/start", "/admin/setup/dismiss", "/admin/setup/finish", "/admin/setup/company"} {
		if w := do(h, "POST", p, url.Values{"to": {"new"}}, c); w.Code != http.StatusForbidden {
			t.Errorf("POST %s: got %d, want 403", p, w.Code)
		}
	}
	body := do(h, "GET", "/admin", nil, c).Body.String()
	for _, banned := range []string{"setup-modal-title", `href="/admin/setup"`, "Setup cepat"} {
		if strings.Contains(body, banned) {
			t.Errorf("Admin dashboard shows %q", banned)
		}
	}
}

// A fresh SuperAdmin sees the dialog and the sidebar item; Lewati hides both for good.
func TestSetupModalShownOnceAndDismissRemembered(t *testing.T) {
	_, h, _, c := crudApp(t)
	body := do(h, "GET", "/admin", nil, c).Body.String()
	if !strings.Contains(body, `id="setup-modal-title"`) || !strings.Contains(body, "Mulai setup cepat") {
		t.Fatal("fresh SuperAdmin should see the first-run dialog")
	}
	if !strings.Contains(body, `href="/admin/setup"`) {
		t.Error("sidebar item missing while the wizard is not finished")
	}
	if w := do(h, "POST", "/admin/setup/dismiss", nil, c); w.Code != http.StatusSeeOther {
		t.Fatalf("dismiss: %d", w.Code)
	}
	body = do(h, "GET", "/admin", nil, c).Body.String()
	if strings.Contains(body, "setup-modal-title") || strings.Contains(body, `href="/admin/setup"`) {
		t.Error("dismissed wizard is still shown")
	}
}

// Starting the wizard from the dialog keeps the sidebar item but not the dialog.
func TestSetupStartedHidesDialog(t *testing.T) {
	_, h, _, c := crudApp(t)
	if w := do(h, "POST", "/admin/setup/start", url.Values{"to": {""}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", w.Code)
	}
	body := do(h, "GET", "/admin", nil, c).Body.String()
	if strings.Contains(body, "setup-modal-title") {
		t.Error("dialog shown again after the wizard was started")
	}
	if !strings.Contains(body, `href="/admin/setup"`) {
		t.Error("sidebar item missing after start")
	}
}

// The progress badge counts required steps from the rows and settings, not from clicks.
func TestSetupProgressBadgeCountsRows(t *testing.T) {
	_, h, q, c := crudApp(t)
	ctx := t.Context()
	if body := do(h, "GET", "/admin", nil, c).Body.String(); !strings.Contains(body, ">0/8<") {
		t.Fatal("empty install should show 0/8")
	}
	if _, err := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "10 Mbps", RateDown: 10, RateDownUnit: "Mbps", RateUp: 10, RateUpUnit: "Mbps"}); err != nil {
		t.Fatal(err)
	}
	// data exists, so welcome and speed are done
	if body := do(h, "GET", "/admin", nil, c).Body.String(); !strings.Contains(body, ">2/8<") {
		t.Fatal("a speed row should count welcome and speed: 2/8")
	}
	for k, v := range map[string]string{"company_name": "Acme ISP", "phone": "0812"} {
		if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	if body := do(h, "GET", "/admin", nil, c).Body.String(); !strings.Contains(body, ">3/8<") {
		t.Error("company name and phone should make the badge 3/8")
	}
}

// Revisiting the NAS step edits the NAS with the same IP; the commands carry the saved secret once.
func TestSetupNASRevisitEditsInsteadOfDuplicating(t *testing.T) {
	_, h, q, c := crudApp(t)
	secret := "Xk72-mQ9pL0vT4wZ"
	form := url.Values{"router_mode": {"radius"}, "nas_name": {"mikrotik-a"}, "nas_ip": {"10.0.0.1"}, "nas_secret": {secret}}
	w := do(h, "POST", "/admin/setup/router", form, c)
	if w.Code != http.StatusOK {
		t.Fatalf("first save: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/radius add service=hotspot,ppp address=") || !strings.Contains(body, "secret="+secret) {
		t.Error("RouterOS command does not carry the NAS secret")
	}
	form.Set("nas_name", "mikrotik-a2")
	form.Del("nas_secret")
	if w := do(h, "POST", "/admin/setup/router", form, c); w.Code != http.StatusOK {
		t.Fatalf("second save: %d", w.Code)
	}
	nas, err := q.ListNAS(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(nas) != 1 || nas[0].Name != "mikrotik-a2" {
		t.Fatalf("NAS rows %+v, want one row renamed to mikrotik-a2", nas)
	}
}

// Speed presets and plan templates are created once; a second run adds nothing.
func TestSetupPresetSpeedsAndPlans(t *testing.T) {
	_, h, q, c := crudApp(t)
	ctx := t.Context()
	speeds := url.Values{"speed_0": {"5"}, "speed_1": {"10"}, "speed_2": {"20"}}
	for i := 0; i < 2; i++ {
		if w := do(h, "POST", "/admin/setup/bandwidth", speeds, c); w.Code != http.StatusSeeOther {
			t.Fatalf("speeds run %d: %d", i, w.Code)
		}
	}
	bws, err := q.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(bws) != 3 {
		t.Fatalf("got %d speeds, want 3", len(bws))
	}
	var tenID int64
	for _, b := range bws {
		if b.Name == "10 Mbps" {
			tenID = b.ID
		}
	}
	plans := url.Values{"plan_bandwidth": {strconv.FormatInt(tenID, 10)}, "price_0": {"5000"}, "price_1": {"25000"}, "price_2": {"100000"}}
	for i := 0; i < 2; i++ {
		if w := do(h, "POST", "/admin/setup/plan", plans, c); w.Code != http.StatusSeeOther {
			t.Fatalf("plans run %d: %d", i, w.Code)
		}
	}
	rows, err := q.ListPlans(ctx, db.ListPlansParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Harian": "Days", "Mingguan": "Days", "Bulanan": "Months"}
	if len(rows) != len(want) {
		t.Fatalf("got %d plans, want %d", len(rows), len(want))
	}
	for _, p := range rows {
		if want[p.Name] != p.ValidityUnit || p.Device != "Radius" {
			t.Errorf("plan %s: unit %q device %q", p.Name, p.ValidityUnit, p.Device)
		}
	}
}

// Skipping a step goes to the next one without saving.
func TestSetupSkipGoesOn(t *testing.T) {
	_, h, _, c := crudApp(t)
	w := do(h, "POST", "/admin/setup/company/skip", nil, c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/setup/router" {
		t.Fatalf("skip company: %d %q", w.Code, w.Header().Get("Location"))
	}
}

// The live JSON reports a RADIUS packet from the NAS, read like the status page does.
func TestSetupLiveReportsNASPacket(t *testing.T) {
	_, h, _, c := crudApp(t)
	metrics.Set("radius_nas_last_packet_timestamp", float64(time.Now().Unix()), "nas", "10.7.7.7")
	if body := do(h, "GET", "/admin/setup/live.json?nas=10.7.7.7", nil, c).Body.String(); !strings.Contains(body, `"nas_seen":true`) {
		t.Errorf("NAS with a packet not reported: %s", body)
	}
	if body := do(h, "GET", "/admin/setup/live.json?nas=10.7.7.8", nil, c).Body.String(); !strings.Contains(body, `"nas_seen":false`) {
		t.Errorf("silent NAS reported as seen: %s", body)
	}
}

// The test voucher is made once, and the finish page shows its code.
func TestSetupVoucherAndFinish(t *testing.T) {
	_, h, q, c := crudApp(t)
	ctx := t.Context()
	if _, err := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "5 Mbps", RateDown: 5, RateDownUnit: "Mbps", RateUp: 5, RateUpUnit: "Mbps"}); err != nil {
		t.Fatal(err)
	}
	bws, _ := q.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 10})
	plan := url.Values{"plan_bandwidth": {strconv.FormatInt(bws[0].ID, 10)}, "price_0": {"5000"}}
	if w := do(h, "POST", "/admin/setup/plan", plan, c); w.Code != http.StatusSeeOther {
		t.Fatalf("plan: %d", w.Code)
	}
	plans, _ := q.ListPlans(ctx, db.ListPlansParams{Limit: 10})
	voucher := url.Values{"voucher_plan": {strconv.FormatInt(plans[0].ID, 10)}}
	for i := 0; i < 2; i++ {
		if w := do(h, "POST", "/admin/setup/customer", voucher, c); w.Code != http.StatusSeeOther {
			t.Fatalf("voucher run %d: %d", i, w.Code)
		}
	}
	vs, err := q.ListVouchers(ctx, db.ListVouchersParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("got %d vouchers, want 1", len(vs))
	}
	body := do(h, "GET", "/admin/setup/finish", nil, c).Body.String()
	if !strings.Contains(body, vs[0].Code) {
		t.Errorf("finish page does not show the test voucher %s", vs[0].Code)
	}
	if w := do(h, "POST", "/admin/setup/finish", nil, c); w.Code != http.StatusSeeOther {
		t.Fatalf("finish: %d", w.Code)
	}
	if body := do(h, "GET", "/admin", nil, c).Body.String(); strings.Contains(body, `href="/admin/setup"`) {
		t.Error("finished wizard still in the sidebar")
	}
}

// Every step page renders for a SuperAdmin, and an imported step is shown for verification.
func TestSetupEveryStepRenders(t *testing.T) {
	_, h, q, c := crudApp(t)
	if w := do(h, "GET", "/admin/setup", nil, c); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/setup/welcome" {
		t.Fatalf("index: %d %q", w.Code, w.Header().Get("Location"))
	}
	for _, st := range wizSteps {
		if w := do(h, "GET", "/admin/setup/"+st.Slug, nil, c); w.Code != http.StatusOK {
			t.Errorf("step %s: %d", st.Slug, w.Code)
		}
	}
	if _, err := q.CreateBandwidth(t.Context(), db.CreateBandwidthParams{Name: "5 Mbps", RateDown: 5, RateDownUnit: "Mbps", RateUp: 5, RateUpUnit: "Mbps"}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: setupModeKey, Value: "import"}); err != nil {
		t.Fatal(err)
	}
	if body := do(h, "GET", "/admin/setup/bandwidth", nil, c).Body.String(); !strings.Contains(body, "Verifikasi") {
		t.Error("imported speed should be shown for verification")
	}
}
