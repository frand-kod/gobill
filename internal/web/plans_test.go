package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

// newPlanForm returns the new plan form with the default device setting set to dev.
func newPlanForm(t *testing.T, dev string) string {
	t.Helper()
	_, h, q, c := crudApp(t)
	if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "default_plan_device", Value: dev}); err != nil {
		t.Fatal(err)
	}
	w := do(h, "GET", "/admin/plans/new", nil, c)
	if w.Code != http.StatusOK {
		t.Fatalf("new plan: %d", w.Code)
	}
	return w.Body.String()
}

func TestNewPlanPreselectsDefaultDevice(t *testing.T) {
	if body := newPlanForm(t, "Radius"); !strings.Contains(body, `value="Radius" selected`) {
		t.Fatal("Radius default not preselected")
	}
}

func TestNewPlanMikrotikDefaultFallsBackToPlanType(t *testing.T) {
	// the new plan form starts as a Hotspot plan, so a PPPoE driver falls back to the Hotspot one
	if body := newPlanForm(t, "MikrotikPppoe"); !strings.Contains(body, `value="MikrotikHotspot" selected`) {
		t.Fatal("Hotspot plan did not fall back to MikrotikHotspot")
	}
}

func TestNewPlanDeviceByType(t *testing.T) {
	cases := []struct{ setting, typ, want string }{
		{"", "Hotspot", ""},
		{"MikrotikHotspot", "PPPoE", "MikrotikPppoe"},
		{"MikrotikPppoe", "PPPoE", "MikrotikPppoe"},
		{"Dummy", "Balance", ""},
		{"Dummy", "PPPoE", "Dummy"},
	}
	for _, c := range cases {
		if got := newPlanDevice(c.setting, c.typ); got != c.want {
			t.Errorf("newPlanDevice(%q, %q) = %q, want %q", c.setting, c.typ, got, c.want)
		}
	}
}
