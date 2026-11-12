package adminx

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/wg"
)

// SettingsTabs are the sub-pages in menu order. Integrations is SuperAdmin only; the rest
// are SuperAdmin and Admin, as in the old settings controller. Payment waits for Tripay.
var SettingsTabs = []struct {
	Slug, Label string
	SuperOnly   bool
}{
	{"app", "General", false},
	{"localisation", "Localisation", false},
	{"billing", "Customers & Billing", false},
	{"payment", "Payments", true},
	{"integrations", "Integrations", true},
	{"notifications", "Notifications", false},
	{"isolir", "Isolation", true},
	{"wireguard", "WireGuard", true},
	{"promo", "Promotions", true},
	{"appearance", "Appearance", false},
	{"system", "System & Security", false},
}

// SettingsNav is the sub-page menu for the caller's role (SuperOnly tabs only for SuperAdmin).
func SettingsNav(r *http.Request) []core.Option {
	var nav []core.Option
	for _, t := range SettingsTabs {
		if (!t.SuperOnly || core.AdminFrom(r).Role == "SuperAdmin") && (t.Slug != "wireguard" || wg.Built) {
			nav = append(nav, core.Option{Value: "/admin/settings/" + t.Slug, Label: t.Label})
		}
	}
	return nav
}
