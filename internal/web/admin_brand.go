package web

// Branding the layouts show.

import (
	"log/slog"

	"context"
	"strconv"
)

// brand returns the branding the layouts show: logo, favicon and login page text, plus
// disable_registration for the portal's register link.
func (s *Server) brand(ctx context.Context) map[string]string {
	out := map[string]string{}
	m, err := s.LoadSettings(ctx)
	if err != nil {
		slog.Error("load branding", "err", err)
		return out
	}
	out["company_name"] = m["company_name"]
	for _, k := range []string{"logo", "logo_dark", "login_page_logo", "login_page_logo_dark", "login_page_favicon", "login_page_head", "login_page_description", "login_page_wallpaper", "disable_registration"} {
		out[k] = m[k]
	}
	// the logo each layout shows, light and dark; a missing dark one falls back to the light one
	pair := func(light, dark string) (string, string) {
		if light == "" {
			light = dark
		}
		if dark == light {
			dark = ""
		}
		return light, dark
	}
	out["logo_a"], out["logo_a_dark"] = pair(m["logo"], m["logo_dark"])
	if m["login_page_logo"] != "" {
		out["logo_l"], out["logo_l_dark"] = pair(m["login_page_logo"], m["login_page_logo_dark"])
	} else {
		out["logo_l"], out["logo_l_dark"] = out["logo_a"], out["logo_a_dark"]
	}
	out["theme_mode"], out["theme_accent"], out["theme_density"] = themeDefaults(m)
	out["theme_font"] = themeFont(m)
	out["phone"], out["wa"] = m["phone"], waLink(m["phone"], m["country_code_phone"]) // operator contact for the portal login
	out["pass_min"] = strconv.Itoa(customerPasswordMin(m))                            // minlength of the portal password fields
	out["theme_color"], _ = pwaColors(out["theme_accent"], out["theme_mode"])
	if out["app_icon"] = pwaIcon(m); out["app_icon"] == "" {
		out["app_icon"] = "/static/icons/app-192.png"
	}
	return out
}
