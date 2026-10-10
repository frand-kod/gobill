package web

import (
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

func TestLoginPagesBrandAndFields(t *testing.T) {
	e := billApp(t)
	for k, v := range map[string]string{"company_name": "Lintas Net", "login_page_wallpaper": "00000000000000000000000000000000.png",
		"phone": "0812 3456 7890", "country_code_phone": "62"} {
		if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		path, action string
		extra        []string
	}{
		{"/login", `action="/login"`, nil},
		{"/portal/login", `action="/portal/login"`, []string{"https://wa.me/628123456789", "/portal/forgot", "/portal/register"}},
	} {
		w := do(e.h, "GET", c.path, nil, nil)
		b := w.Body.String()
		if w.Code != 200 {
			t.Fatalf("%s: %d", c.path, w.Code)
		}
		for _, want := range append([]string{c.action, `name="username"`, `name="password"`, `autocomplete="username"`, `autocomplete="current-password"`,
			"Lintas Net", "/uploads/00000000000000000000000000000000.png", `aria-pressed="false"`}, c.extra...) {
			if !strings.Contains(b, want) {
				t.Errorf("%s: missing %q", c.path, want)
			}
		}
		if strings.Contains(strings.ToLower(b), "nuxbill") {
			t.Errorf("%s: contains NuxBill", c.path)
		}
	}
}
