package web

import (
	"regexp"
	"strings"
	"testing"
)

// navKeys is the set of keys app.html passes to navActive, in the same order.
var navKeys = []string{"/admin", "/admin/docs", "/admin/customers", "/admin/recharge", "/admin/vouchers/redeem", "/admin/deposit", "/admin/vouchers", "/admin/subscriptions", "/admin/radius", "/admin/message", "/admin/reports", "/admin/reports/period", "/admin/transactions", "/admin/plans", "/admin/bandwidth", "/admin/pool", "/admin/coupons", "/admin/routers", "/admin/nas", "/admin/maps/customers", "/admin/maps/routers", "/admin/maps/odp", "/admin/odp", "/admin/payment-gateway", "/admin/logs", "/admin/users", "/admin/pages", "/admin/fields", "/admin/settings"}

func TestNavActiveResolution(t *testing.T) {
	cases := map[string]string{
		"/admin":                        "/admin",
		"/admin/unknown":                "",
		"/admin/customers":              "/admin/customers",
		"/admin/customers/123":          "/admin/customers",
		"/admin/vouchers":               "/admin/vouchers",
		"/admin/vouchers/new":           "/admin/vouchers",
		"/admin/vouchers/print":         "/admin/vouchers",
		"/admin/vouchers/redeem":        "/admin/vouchers/redeem",
		"/admin/reports":                "/admin/reports",
		"/admin/reports/period":         "/admin/reports/period",
		"/admin/plans/new":              "/admin/plans",
		"/admin/settings/app":           "/admin/settings",
		"/admin/settings/notifications": "/admin/settings",
		"/admin/radius/sessions":        "/admin/radius",
		"/admin/message/send":           "/admin/message",
		"/admin/pages/announcement":     "/admin/pages",
		"/admin/fields/customer":        "/admin/fields",
		"/admin/docs/billing":           "/admin/docs",
		"/admin/customersx":             "",
		"/admin/settingsx":              "",
	}
	for path, want := range cases {
		if got := navActive(path, navKeys...); got != want {
			t.Errorf("navActive(%q) = %q, want %q", path, got, want)
		}
	}
}

// Only one nav entry may carry aria-current, and it must be the one for the longest matching href.
func TestNavOneActiveItem(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	c := login(t, h, "alice")
	re := regexp.MustCompile(`<a class="nav-item" href="([^"]+)"[^>]*aria-current="page"`)
	for path, wantHref := range map[string]string{
		"/admin/vouchers/redeem": "/admin/vouchers/redeem",
		"/admin/vouchers/new":    "/admin/vouchers",
		"/admin/settings/app":    "/admin/settings",
	} {
		body := do(h, "GET", path, nil, c).Body.String()
		start, end := strings.Index(body, `<nav id="gb-nav"`), strings.Index(body, "</nav>")
		if start < 0 || end < start {
			t.Fatalf("%s: sidebar nav missing", path)
		}
		m := re.FindAllStringSubmatch(body[start:end], -1)
		if len(m) != 1 || m[0][1] != wantHref {
			t.Errorf("%s: active nav items %v, want only %s", path, m, wantHref)
		}
	}
}

func TestFooterNoGuideNoDoubleV(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	c := login(t, h, "alice")
	for version, want := range map[string]string{"v0.1.1": "v0.1.1", "0.1.1": "v0.1.1", "dev": "dev"} {
		s.Version = version
		body := do(h, "GET", "/admin", nil, c).Body.String()
		start := strings.Index(body, "<footer")
		end := strings.Index(body[start:], "</footer>")
		if start < 0 || end < 0 {
			t.Fatal("footer missing")
		}
		footer := body[start : start+end]
		if strings.Contains(footer, "/admin/docs") {
			t.Error("footer still links the user guide")
		}
		if strings.Contains(footer, "vv") || strings.Contains(footer, "vdev") {
			t.Errorf("footer doubles the version prefix: %s", footer)
		}
		if !strings.Contains(footer, want) {
			t.Errorf("version %q: footer missing %q", version, want)
		}
	}
}

// The sidebar keeps its place across full page loads: every group has an id, the current page's group is marked,
// the restore script is inline, and app.css carries the cross-document view transition.
func TestAdminSidebarKeepsPlace(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	c := login(t, h, "alice")
	body := do(h, "GET", "/admin/reports", nil, c).Body.String()
	for _, id := range []string{"reports", "plans", "network", "maps", "admin"} {
		if !strings.Contains(body, `data-gb-group="`+id+`"`) {
			t.Errorf("sidebar group %q has no id", id)
		}
	}
	if !strings.Contains(body, `data-gb-group="reports" data-gb-active`) {
		t.Error("Reports group not marked active on /admin/reports")
	}
	for _, want := range []string{`id="gb-sidebar"`, `id="gb-topbar"`, `gb-nav-scroll`, `gb-nav-open`} {
		if !strings.Contains(body, want) {
			t.Errorf("admin page missing %s", want)
		}
	}
	css := do(h, "GET", "/static/app.css", nil, nil).Body.String()
	for _, want := range []string{"@view-transition{navigation:auto}", "#gb-sidebar{view-transition-name:sidebar}", "prefers-reduced-motion:reduce"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css missing %q", want)
		}
	}
}

// Sidebar icon hues: each group carries its nav-hue class, the top items share one wrapper, and app.css
// defines every --nav-hue token with both its light and its dark value.
func TestSidebarGroupHueClasses(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	c := login(t, h, "alice")
	body := do(h, "GET", "/admin", nil, c).Body.String()
	if !strings.Contains(body, `<div class="nav-hue-top">`) {
		t.Error("top sidebar items are not wrapped in nav-hue-top")
	}
	for _, hue := range []string{"reports", "plans", "network", "maps", "admin"} {
		if !strings.Contains(body, `class="nav-hue-`+hue+`"`) {
			t.Errorf("sidebar group %q has no nav-hue-%s class", hue, hue)
		}
	}
	css := do(h, "GET", "/static/app.css", nil, nil).Body.String()
	for hue, pair := range map[string][2]string{
		"top": {"#5f7f7b", "#a8c4c0"}, "reports": {"#8c6d2e", "#d4bb85"}, "plans": {"#7a6aa8", "#b9adde"},
		"network": {"#4b7aa0", "#9dc4e2"}, "maps": {"#5a7f5e", "#a6cba9"}, "admin": {"#8e5f6e", "#d9a9b8"},
	} {
		for _, v := range pair {
			if want := "--nav-hue-" + hue + ":" + v; !strings.Contains(css, want) {
				t.Errorf("app.css missing %s", want)
			}
		}
	}
}
