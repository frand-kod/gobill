package web

// Theme defaults (mode, accent, density) and validation.

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"regexp"
	"strings"
)

// Theme defaults live in the settings table (theme_mode, theme_accent, theme_density). Admins without
// a local choice and the customer portal use them; the Tema popover stores a local choice per browser.
var (
	themeModes     = map[string]bool{"light": true, "dark": true, "system": true}
	themeAccents   = map[string]bool{"teal": true, "orange": true}
	themeDensities = map[string]bool{"compact": true, "normal": true, "roomy": true}
	themeFonts     = map[string]bool{"small": true, "medium": true, "large": true} // root font size 14/15/17px
	themeHex       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

func validAccent(a string) bool { return themeAccents[a] || themeHex.MatchString(a) }

// themeDefaults returns the stored defaults, falling back to system/teal/normal for anything missing or invalid.
func themeDefaults(m map[string]string) (mode, accent, density string) {
	mode, accent, density = "system", "teal", "normal"
	if themeModes[m["theme_mode"]] {
		mode = m["theme_mode"]
	}
	if a := strings.ToLower(m["theme_accent"]); validAccent(a) {
		accent = a
	}
	if themeDensities[m["theme_density"]] {
		density = m["theme_density"]
	}
	return
}

// themeFont returns the stored default text size, "medium" when missing or invalid.
func themeFont(m map[string]string) string {
	if themeFonts[m["theme_font"]] {
		return m["theme_font"]
	}
	return "medium"
}

// themeDefault saves the defaults for everyone. SuperAdmin only (route), values checked strictly.
func (s *Server) themeDefault(w http.ResponseWriter, r *http.Request) {
	mode, accent, density, font := r.FormValue("mode"), strings.ToLower(r.FormValue("accent")), r.FormValue("density"), r.FormValue("font")
	if !themeModes[mode] || !validAccent(accent) || !themeDensities[density] || !themeFonts[font] {
		http.Error(w, "Invalid theme", http.StatusUnprocessableEntity)
		return
	}
	for k, v := range map[string]string{"theme_mode": mode, "theme_accent": accent, "theme_density": density, "theme_font": font} {
		if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			s.fail(w, "save "+k, err)
			return
		}
	}
	back := r.FormValue("next")
	if !strings.HasPrefix(back, "/admin") || strings.ContainsAny(back, "\\\r\n") || strings.Contains(back, "//") {
		back = "/admin"
	}
	s.done(w, r, back, "Theme default saved", "theme.default", mode+" "+accent+" "+density+" "+font)
}
