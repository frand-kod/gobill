package core

// Theme colours and the app icon the layouts and manifests share.

import "strings"

var AccentColors = map[string]string{"teal": "#0b7f79", "orange": "#f38020"}

// PwaColors returns the theme colour (the accent) and the background colour from the theme defaults.
func PwaColors(accent, mode string) (theme, bg string) {
	if theme = AccentColors[accent]; theme == "" {
		theme = strings.ToLower(accent)
	}
	if theme == "" {
		theme = AccentColors["teal"]
	}
	bg = "#eceef2"
	if mode == "dark" {
		bg = "#171b21"
	}
	return
}

// PwaIcon is the uploaded favicon or logo (png, jpg, webp) if there is one, else "" for the shipped default.
func PwaIcon(m map[string]string) string {
	for _, k := range []string{"login_page_favicon", "logo"} {
		if f := m[k]; f != "" && !strings.HasSuffix(f, ".ico") {
			return "/uploads/" + f
		}
	}
	return ""
}
