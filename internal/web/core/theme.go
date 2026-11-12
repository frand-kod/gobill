package core

// Theme defaults (mode, accent, density) and validation.

import (
	"regexp"
	"strings"
)

// Theme defaults live in the settings table (theme_mode, theme_accent, theme_density). Admins without
// a local choice and the customer portal use them; the Tema popover stores a local choice per browser.
var (
	ThemeModes     = map[string]bool{"light": true, "dark": true, "system": true}
	ThemeAccents   = map[string]bool{"teal": true, "orange": true}
	ThemeDensities = map[string]bool{"compact": true, "normal": true, "roomy": true}
	ThemeFonts     = map[string]bool{"small": true, "medium": true, "large": true} // root font size 14/15/17px
	ThemeHex       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

func ValidAccent(a string) bool { return ThemeAccents[a] || ThemeHex.MatchString(a) }

// ThemeDefaults returns the stored defaults, falling back to system/teal/normal for anything missing or invalid.
func ThemeDefaults(m map[string]string) (mode, accent, density string) {
	mode, accent, density = "system", "teal", "normal"
	if ThemeModes[m["theme_mode"]] {
		mode = m["theme_mode"]
	}
	if a := strings.ToLower(m["theme_accent"]); ValidAccent(a) {
		accent = a
	}
	if ThemeDensities[m["theme_density"]] {
		density = m["theme_density"]
	}
	return
}

// ThemeFont returns the stored default text size, "medium" when missing or invalid.
func ThemeFont(m map[string]string) string {
	if ThemeFonts[m["theme_font"]] {
		return m["theme_font"]
	}
	return "medium"
}
