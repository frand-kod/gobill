package settings

import (
	"net/http"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/core"
)

// themeDefault saves the defaults for everyone. SuperAdmin only (route), values checked strictly.
func (s *Handlers) themeDefault(w http.ResponseWriter, r *http.Request) {
	mode, accent, density, font := r.FormValue("mode"), strings.ToLower(r.FormValue("accent")), r.FormValue("density"), r.FormValue("font")
	if !core.ThemeModes[mode] || !core.ValidAccent(accent) || !core.ThemeDensities[density] || !core.ThemeFonts[font] {
		http.Error(w, "Invalid theme", http.StatusUnprocessableEntity)
		return
	}
	for k, v := range map[string]string{"theme_mode": mode, "theme_accent": accent, "theme_density": density, "theme_font": font} {
		if err := s.Queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			s.Fail(w, "save "+k, err)
			return
		}
	}
	back := r.FormValue("next")
	if !strings.HasPrefix(back, "/admin") || strings.ContainsAny(back, "\\\r\n") || strings.Contains(back, "//") {
		back = "/admin"
	}
	s.Done(w, r, back, "Theme default saved", "theme.default", mode+" "+accent+" "+density+" "+font)
}
