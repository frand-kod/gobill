package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
)

// isolirPlanSetup is the admin app with the isolir plan made (as turning Isolir on does), a public
// plan Gold on a 10M profile, and a customer budi who can be recharged.
func isolirPlanSetup(t *testing.T) (h http.Handler, c *http.Cookie, q *db.Queries, svc *billing.Service, isoID int64, gold db.Plan, cust db.Customer) {
	t.Helper()
	s, h, q := settingsSetup(t)
	svc = &billing.Service{DB: s.conn, Q: q, Key: make([]byte, 32), Loc: time.UTC}
	s.Billing = svc
	ctx := t.Context()
	isoID, err := isolir.EnsurePlan(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	bw, err := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "Gold 10M", RateDown: 10, RateDownUnit: "Mbps", RateUp: 5, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	gold, err = q.CreatePlan(ctx, db.CreatePlanParams{Name: "Gold", Type: "Hotspot", Billing: "prepaid", Price: 150000,
		Validity: 30, ValidityUnit: "Days", Enabled: 1, Device: "Radius", BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	cust, err = q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "budi", PasswordHash: "h", Fullname: "Budi", ServiceType: "Hotspot", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	return h, login(t, h, "alice"), q, svc, isoID, gold, cust
}

// The isolir plan and its bandwidth are not offered by any list or select, but Gold still is.
func TestIsolirPlanHiddenFromLists(t *testing.T) {
	h, c, _, _, isoID, gold, _ := isolirPlanSetup(t)
	isoOpt := fmt.Sprintf(`<option value="%d"`, isoID)
	goldOpt := fmt.Sprintf(`<option value="%d"`, gold.ID)
	for _, p := range []string{"/admin/vouchers/new", "/admin/recharge", "/admin/plans/new"} {
		body := getBody(t, h, c, p)
		if strings.Contains(body, isoOpt) {
			t.Errorf("%s offers the isolir plan", p)
		}
		if p != "/admin/plans/new" && !strings.Contains(body, goldOpt) {
			t.Errorf("%s lost the public plan", p)
		}
	}
	list := getBody(t, h, c, "/admin/plans")
	if strings.Contains(list, fmt.Sprintf("/admin/plans/%d/edit", isoID)) || !strings.Contains(list, fmt.Sprintf("/admin/plans/%d/edit", gold.ID)) {
		t.Error("plan list shows the isolir plan or misses Gold")
	}
	if !strings.Contains(list, `href="/admin/settings/isolir"`) || !strings.Contains(list, "Isolir plan is managed in Settings") {
		t.Error("plan list lacks the Isolir settings line")
	}
	if bws := getBody(t, h, c, "/admin/bandwidth"); strings.Contains(bws, isolir.BandwidthName) || !strings.Contains(bws, "Gold 10M") {
		t.Error("bandwidth list shows the isolir profile or misses Gold 10M")
	}
}

// Nothing can assign the isolir plan by hand: recharge, vouchers and the billing service reject it.
func TestIsolirPlanRejectedOnWrite(t *testing.T) {
	h, c, q, svc, isoID, _, cust := isolirPlanSetup(t)
	ctx := t.Context()
	form := url.Values{"customer": {"budi"}, "plan": {fmt.Sprint(isoID)}, "method": {"Cash"}}
	if w := do(h, "POST", "/admin/recharge", form, c); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("recharge with the isolir plan: %d", w.Code)
	}
	vf := url.Values{"plan": {fmt.Sprint(isoID)}, "numbervoucher": {"2"}, "voucher_format": {"up"}, "lengthcode": {"8"}}
	if w := do(h, "POST", "/admin/vouchers", vf, c); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("vouchers for the isolir plan: %d", w.Code)
	}
	if vs, _ := q.ListVouchers(ctx, db.ListVouchersParams{Limit: 10}); len(vs) != 0 {
		t.Fatalf("%d vouchers made for the isolir plan", len(vs))
	}
	if err := svc.Recharge(ctx, cust.ID, isoID, "Admin - Cash", 0); err != billing.ErrIsolirPlan {
		t.Fatalf("service recharge with the isolir plan: %v", err)
	}
}

// The Isolir speed fields save to the isolir bandwidth row and are shown back from it.
func TestIsolirSpeedSavesBandwidth(t *testing.T) {
	h, c, q, _, isoID, _, _ := isolirPlanSetup(t)
	ctx := t.Context()
	form := url.Values{"isolir_enabled": {"yes"}, "isolir_days": {"30"}, "isolir_title": {"T"},
		"isolir_message": {"M"}, "isolir_wa": {""}, "isolir_show_qris": {"yes"}, "isolir_show_voucher": {"yes"},
		"isolir_address_list": {"isolir"}, "isolir_mode": {"default"}, "isolir_html": {""},
		"isolir_rate_down": {"2M"}, "isolir_rate_up": {"512k"}}
	if w := do(h, "POST", "/admin/settings/isolir", form, c); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	p, err := q.GetPlan(ctx, isoID)
	if err != nil {
		t.Fatal(err)
	}
	bw, err := q.GetBandwidth(ctx, p.BandwidthID.Int64)
	if err != nil {
		t.Fatal(err)
	}
	if bw.RateDown != 2 || bw.RateDownUnit != "Mbps" || bw.RateUp != 512 || bw.RateUpUnit != "Kbps" {
		t.Fatalf("bandwidth row: %+v", bw)
	}
	body := getBody(t, h, c, "/admin/settings/isolir")
	for _, want := range []string{`name="isolir_rate_down" type="text" value="2M"`, `name="isolir_rate_up" type="text" value="512k"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %s", want)
		}
	}
	if strings.Contains(body, `name="isolir_plan_id"`) {
		t.Error("the plan select is still on the Isolir settings")
	}
	form["isolir_rate_down"] = []string{"fast"}
	if w := do(h, "POST", "/admin/settings/isolir", form, c); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad rate: %d", w.Code)
	}
}
