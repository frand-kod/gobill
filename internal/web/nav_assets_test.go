package web

import (
	"os"
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

// Settings tabs: the tab links sit inside #tab-content, which nav.js swaps in place; the form and its tabs
// both come back in that block.
func TestSettingsTabsSwapContainer(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	body := do(h, "GET", "/admin/settings/app", nil, c).Body.String()
	start := strings.Index(body, `<div id="tab-content"><nav`)
	end := strings.Index(body, `<footer`)
	if start < 0 || end < start {
		t.Fatal("settings page lacks the #tab-content block")
	}
	block := body[start:end]
	for _, slug := range []string{"app", "notifications", "payment"} {
		if !strings.Contains(block, `href="/admin/settings/`+slug+`"`) {
			t.Errorf("tab link %s is not inside #tab-content", slug)
		}
	}
	if !strings.Contains(block, `<h1 class="page-title" tabindex="-1">`) || !strings.Contains(block, `<form method="post"`) {
		t.Error("heading (focus target) or form missing from #tab-content")
	}
}

// No flash: the theme is applied by an inline script in <head>, ahead of the stylesheet, and the built
// stylesheet sets color-scheme and the page background on html.
func TestThemeAppliedBeforeStylesheet(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	body := do(h, "GET", "/admin", nil, c).Body.String()
	head, end := strings.Index(body, "<head>"), strings.Index(body, "</head>")
	theme, css := strings.Index(body, "window.gbThemeCtl"), strings.Index(body, `<link rel="stylesheet" href="/static/app.css">`)
	if head < 0 || end < head || theme < head || theme > css || css > end {
		t.Fatalf("theme script must sit in <head> before app.css (head %d, theme %d, css %d, end %d)", head, theme, css, end)
	}
	built, err := os.ReadFile("../../web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"html{background:var(--bg)}", "color-scheme:light", "gb-vt-in"} {
		if !strings.Contains(string(built), want) {
			t.Errorf("app.css lacks %q; run tools/tailwind.sh", want)
		}
	}
}
