package web

import (
	"strings"
	"testing"
)

// Active / Expired and Customer share one tile: both values sit in the split tile, and no separate
// Customer tile is left over. Labels are translated, so the test checks the markup, not the words.
func TestDashboardSplitTileHasBothParts(t *testing.T) {
	e := billApp(t)
	body := do(e.h, "GET", "/admin", nil, e.c).Body.String()
	if n := strings.Count(body, `id="tile-customers"`); n != 1 {
		t.Fatalf("customer count tiles: %d, want 1", n)
	}
	start := strings.Index(body, `class="stat stat-split"`)
	if start < 0 {
		t.Fatal("split tile missing")
	}
	end := strings.Index(body[start:], `class="stat"`)
	if end < 0 {
		t.Fatal("no tile after the split tile")
	}
	tile := body[start : start+end]
	if n := strings.Count(tile, `class="stat-label"`); n != 2 {
		t.Errorf("split tile has %d labels, want 2", n)
	}
	for _, want := range []string{`id="tile-subs"`, `id="tile-customers"`, `href="/admin/customers"`, `class="stat-sep"`} {
		if !strings.Contains(tile, want) {
			t.Errorf("split tile missing %s", want)
		}
	}
}
