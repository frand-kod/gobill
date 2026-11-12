package quota

import "testing"

func TestParseWindowErrors(t *testing.T) {
	for _, bad := range []string{"Mon", "Mon 25:00-26:00", "Foo 08:00-09:00", "08:00-08:00", "Mon 8-9", "Mon 08:00-09:60"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("%q must not parse", bad)
		}
	}
	if r, err := ParseWindow("Fri-Mon 08:00-17:00"); err != nil || len(r) != 1 || r[0].days != 1<<5|1<<6|1<<0|1<<1 {
		t.Errorf("wrapping day range: %v %v", r, err)
	}
}
