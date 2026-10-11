package web

import (
	"strings"
	"testing"
)

// Prefetch: nav.js and the speculation rules load on admin pages, prefetch only (never prerender), and the
// links that change state or cost a lot opt out with data-no-prefetch.
func TestPrefetchWiring(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	body := do(h, "GET", "/admin", nil, c).Body.String()
	for _, want := range []string{`<script type="speculationrules">`, `"prefetch"`, `"eagerness": "moderate"`,
		`"href_matches": "/admin/*"`, `"href_matches": "/portal/*"`, `"selector_matches": "[data-no-prefetch]"`, `<script defer src="/static/nav.js">`} {
		if !strings.Contains(body, want) {
			t.Errorf("/admin page missing %s", want)
		}
	}
	if strings.Contains(body, "prerender") {
		t.Error("speculation rules must not prerender: live pages poll")
	}
	if w := do(h, "GET", "/static/nav.js", nil, nil); w.Code != 200 {
		t.Errorf("/static/nav.js: got %d", w.Code)
	}
	if body := do(h, "GET", "/admin/settings/system", nil, c).Body.String(); !strings.Contains(body, `href="/admin/settings/system/backup" data-no-prefetch`) {
		t.Error("backup download link must be data-no-prefetch")
	}
}
