package diagnose

import "testing"

func TestQuotaLevelAndPct(t *testing.T) {
	for _, c := range []struct {
		used, lim  int64
		level, pct string
	}{{0, 100, "ok", "0%"}, {89, 100, "ok", "89%"}, {90, 100, "warn", "90%"}, {100, 100, "bad", "100%"}, {250, 100, "bad", "100%"}, {5, 0, "bad", ""}} {
		if got := quotaLevel(c.used, c.lim); got != c.level {
			t.Errorf("quotaLevel(%d,%d) = %s, want %s", c.used, c.lim, got, c.level)
		}
		if got := pct(c.used, c.lim); got != c.pct {
			t.Errorf("pct(%d,%d) = %q, want %q", c.used, c.lim, got, c.pct)
		}
	}
}
