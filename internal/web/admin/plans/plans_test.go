package plans

import "testing"

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
