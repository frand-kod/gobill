// Characterization tests for M3 (docs/internal/rencana/modularisasi.md). They pin what the code does
// today, so the merge into one isolir.Service keeps it. Isolir rules live in 4 places:
//
//  1. internal/isolir/isolir.go: Defaults, Value, Days, Until, PlanID, EnsurePlan, AddressListFor,
//     HostLines, ValidHost. Pinned in this file.
//  2. internal/billing/isolir.go: isolate, isolirPlan, endIsolation, closeIsolation, EndIsolations.
//     Pinned in internal/billing/isolir_test.go: start window, skip rules (off, explicit expired
//     plan, non-Radius plan, inactive customer, other active plan, already isolated), end as
//     paid, expired and disabled.
//  3. internal/radius/radius.go isolirPlanFor: needs isolir_enabled "yes", a non-zero
//     isolir_plan_id and a running isolation (ends_at > now). Pinned by
//     TestRadiusRestIsolirAcceptCarriesAddressList in internal/web and by AddressListFor here.
//  4. internal/web/isolir.go: isolirCustomer, isolirIsolated, isolirGate, isolirRouterRules,
//     lanIPv4. Pinned by the TestIsolirGate* and TestIsolirRouterRulesBlock tests in internal/web.
package isolir

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestValueDefaultsAndWAFallback(t *testing.T) {
	st := map[string]string{"phone": "0812", "isolir_days": "7", "isolir_address_list": ""}
	cases := map[string]string{
		"isolir_days":         "7",
		"isolir_enabled":      "no",
		"isolir_mode":         "default",
		"isolir_allow_hosts":  DefaultAllowHosts,
		"isolir_address_list": "", // a stored empty value stays empty; AddressListFor falls back, Value does not
		"isolir_wa":           "0812",
		"unknown_key":         "",
	}
	for k, want := range cases {
		if got := Value(st, k); got != want {
			t.Errorf("Value(%q) = %q, want %q", k, got, want)
		}
	}
	st["isolir_wa"] = ""
	if got := Value(st, "isolir_wa"); got != "0812" {
		t.Errorf("empty isolir_wa = %q, want company phone", got)
	}
	st["isolir_wa"] = "0899"
	if got := Value(st, "isolir_wa"); got != "0899" {
		t.Errorf("isolir_wa = %q", got)
	}
	if Defaults["isolir_address_list"] != DefaultAddrList || Defaults["isolir_days"] != "30" || Defaults["isolir_enabled"] != "no" {
		t.Errorf("defaults changed: %v", Defaults)
	}
}

func TestDaysUntilPlanID(t *testing.T) {
	for in, want := range map[string]int{"": 30, "x": 30, "5": 5, "0": 0, "-3": 0, " 5": 30} {
		if got := Days(in); got != want {
			t.Errorf("Days(%q) = %d, want %d", in, got, want)
		}
	}
	now := time.Date(2025, 1, 31, 12, 0, 0, 0, time.UTC)
	if got := Until(now, 0); !got.Equal(Forever) {
		t.Errorf("Until 0 = %v", got)
	}
	if got := Until(now, -1); !got.Equal(Forever) {
		t.Errorf("Until -1 = %v", got)
	}
	if got := Until(now, 1); !got.Equal(now.AddDate(0, 0, 1)) {
		t.Errorf("Until 1 = %v", got)
	}
	for in, want := range map[string]int64{"": 0, "x": 0, "12": 12, "-4": -4} {
		if got := PlanID(in); got != want {
			t.Errorf("PlanID(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestAddressListFor(t *testing.T) {
	st := map[string]string{"isolir_plan_id": "7"}
	if got := AddressListFor(st, 7); got != DefaultAddrList {
		t.Errorf("unset list = %q", got)
	}
	st["isolir_address_list"] = ""
	if got := AddressListFor(st, 7); got != DefaultAddrList {
		t.Errorf("empty list = %q", got)
	}
	st["isolir_address_list"] = "blok"
	if got := AddressListFor(st, 7); got != "blok" {
		t.Errorf("custom list = %q", got)
	}
	if got := AddressListFor(st, 8); got != "" {
		t.Errorf("other plan = %q", got)
	}
	if got := AddressListFor(map[string]string{}, 0); got != "" {
		t.Errorf("plan 0 with no isolir plan = %q", got)
	}
	// isolir_enabled is not looked at here.
	st["isolir_enabled"] = "no"
	if got := AddressListFor(st, 7); got != "blok" {
		t.Errorf("disabled = %q", got)
	}
}

func TestHostLinesAndValidHost(t *testing.T) {
	got := HostLines(" WA.me \n\n*.WhatsApp.net\r\n")
	if len(got) != 2 || got[0] != "wa.me" || got[1] != "*.whatsapp.net" {
		t.Errorf("HostLines = %q", got)
	}
	for _, h := range HostLines(DefaultAllowHosts) {
		if !ValidHost(h) {
			t.Errorf("default host %q invalid", h)
		}
	}
	for _, h := range []string{"", "*", "*.", "a.*.b", "http://x.com", "x.com/", "x..com", "WA.me", "a b"} {
		if ValidHost(h) {
			t.Errorf("ValidHost(%q) = true", h)
		}
	}
	// Loose today: a leading hyphen and a bare IP pass.
	for _, h := range []string{"localhost", "1.2.3.4", "-x.com"} {
		if !ValidHost(h) {
			t.Errorf("ValidHost(%q) = false", h)
		}
	}
}

func openDB(t *testing.T) *db.Queries {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return db.New(conn)
}

func setPlanID(t *testing.T, q *db.Queries, v string) {
	t.Helper()
	if err := q.UpsertSetting(context.Background(), db.UpsertSettingParams{Key: "isolir_plan_id", Value: v}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsurePlanCreatesOnceAndStoresID(t *testing.T) {
	ctx, q := context.Background(), openDB(t)
	before, _ := q.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
	id, err := EnsurePlan(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	p, err := q.GetPlan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != PlanName || p.Device != "Radius" || p.Type != "Hotspot" || p.Billing != "prepaid" || p.Enabled != 0 || p.Price != 0 || !p.BandwidthID.Valid {
		t.Fatalf("plan %+v", p)
	}
	bw, err := q.GetBandwidth(ctx, p.BandwidthID.Int64)
	if err != nil {
		t.Fatal(err)
	}
	if bw.Name != BandwidthName || bw.RateDown != 64 || bw.RateDownUnit != "Kbps" || bw.RateUp != 64 || bw.RateUpUnit != "Kbps" {
		t.Fatalf("bandwidth %+v", bw)
	}
	if v, _ := q.GetSetting(ctx, "isolir_plan_id"); PlanID(v) != id {
		t.Fatalf("isolir_plan_id = %q, want %d", v, id)
	}
	again, err := EnsurePlan(ctx, q)
	if err != nil || again != id {
		t.Fatalf("second EnsurePlan = %d, %v; want %d", again, err, id)
	}
	after, _ := q.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
	if len(after) != len(before)+1 {
		t.Fatalf("want one new plan, got %d -> %d", len(before), len(after))
	}
}

// A stale isolir_plan_id is replaced: a Radius plan named Isolir is reused, and a legacy bandwidth
// name is reused when the plan is created.
func TestEnsurePlanReusesByNameAndLegacyBandwidth(t *testing.T) {
	ctx, q := context.Background(), openDB(t)
	legacy, err := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: legacyBWName, RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	if err != nil {
		t.Fatal(err)
	}
	setPlanID(t, q, "999")
	id, err := EnsurePlan(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := q.GetPlan(ctx, id); p.BandwidthID.Int64 != legacy.ID {
		t.Fatalf("bandwidth %d, want legacy %d", p.BandwidthID.Int64, legacy.ID)
	}
	setPlanID(t, q, "999")
	again, err := EnsurePlan(ctx, q)
	if err != nil || again != id {
		t.Fatalf("by name = %d, %v; want %d", again, err, id)
	}
	if v, _ := q.GetSetting(ctx, "isolir_plan_id"); PlanID(v) != id {
		t.Fatalf("isolir_plan_id = %q, want %d", v, id)
	}
}

// A stored id is kept when the plan exists, whatever its name, device or enabled flag.
func TestEnsurePlanKeepsStoredPlan(t *testing.T) {
	ctx, q := context.Background(), openDB(t)
	p, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "Topup", Type: "Balance", Billing: "prepaid", Validity: 30, ValidityUnit: "Days", Enabled: 1, Device: "Dummy"})
	if err != nil {
		t.Fatal(err)
	}
	setPlanID(t, q, strconv.FormatInt(p.ID, 10))
	id, err := EnsurePlan(ctx, q)
	if err != nil || id != p.ID {
		t.Fatalf("EnsurePlan = %d, %v; want stored %d", id, err, p.ID)
	}
}
