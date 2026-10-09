package web

// Installable app: web app manifests for /admin and /portal. No service worker: current browsers install
// from the manifest alone, and a worker would only risk caching signed-in pages.

import (
	"encoding/json"
	"mime"
	"net/http"
	"path"
	"strings"
)

var accentColors = map[string]string{"teal": "#0b7f79", "orange": "#f38020"}

// pwaColors returns the theme colour (the accent) and the background colour from the theme defaults.
func pwaColors(accent, mode string) (theme, bg string) {
	if theme = accentColors[accent]; theme == "" {
		theme = strings.ToLower(accent)
	}
	if theme == "" {
		theme = accentColors["teal"]
	}
	bg = "#f6f7f9"
	if mode == "dark" {
		bg = "#171b21"
	}
	return
}

// pwaIcon is the uploaded favicon or logo (png, jpg, webp) if there is one, else "" for the shipped default.
func pwaIcon(m map[string]string) string {
	for _, k := range []string{"login_page_favicon", "logo"} {
		if f := m[k]; f != "" && !strings.HasSuffix(f, ".ico") {
			return "/uploads/" + f
		}
	}
	return ""
}

// manifest serves the web app manifest; suffix is appended to the company name ("" or " Admin").
func (s *Server) manifest(suffix, start, scope string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := s.loadSettings(r.Context())
		if err != nil {
			s.fail(w, "manifest settings", err)
			return
		}
		mode, accent, _ := themeDefaults(st)
		theme, bg := pwaColors(accent, mode)
		company := strings.TrimSpace(st["company_name"])
		if company == "" {
			company = "gobill"
		}
		short := []rune(company)
		if len(short) > 12 {
			short = short[:12]
		}
		icons := []map[string]string{
			{"src": "/static/icons/app-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/static/icons/app-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
		}
		if u := pwaIcon(st); u != "" {
			icons = []map[string]string{{"src": u, "sizes": "192x192 512x512", "type": mime.TypeByExtension(path.Ext(u)), "purpose": "any"}}
		}
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]any{
			"name": company + suffix, "short_name": string(short), "start_url": start, "scope": scope, "display": "standalone",
			"theme_color": theme, "background_color": bg, "icons": icons,
		})
	}
}
