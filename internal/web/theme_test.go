package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestThemeDefaultEndpoint(t *testing.T) {
	_, h, q := settingsSetup(t)
	good := url.Values{"mode": {"dark"}, "accent": {"#12AB34"}, "density": {"roomy"}, "font": {"large"}, "next": {"/admin/customers"}}
	// only SuperAdmin
	if w := do(h, "POST", "/admin/theme/default", good, login(t, h, "bob")); w.Code != http.StatusForbidden {
		t.Fatalf("admin: %d", w.Code)
	}
	if w := do(h, "POST", "/admin/theme/default", good, nil); w.Code == http.StatusSeeOther && w.Header().Get("Location") == "/admin/customers" {
		t.Fatal("anonymous accepted")
	}
	c := login(t, h, "alice")
	for _, bad := range []url.Values{
		{"mode": {"blue"}, "accent": {"teal"}, "density": {"normal"}, "font": {"medium"}},
		{"mode": {"dark"}, "accent": {"red"}, "density": {"normal"}, "font": {"medium"}},
		{"mode": {"dark"}, "accent": {"#12345"}, "density": {"normal"}, "font": {"medium"}},
		{"mode": {"dark"}, "accent": {"#12345g"}, "density": {"normal"}, "font": {"medium"}},
		{"mode": {"dark"}, "accent": {"teal"}, "density": {"huge"}, "font": {"medium"}},
		{"mode": {"dark"}, "accent": {`#123456"><script>`}, "density": {"normal"}, "font": {"medium"}},
		{"mode": {"dark"}, "accent": {"teal"}, "density": {"normal"}, "font": {"huge"}},
		{"mode": {"dark"}, "accent": {"teal"}, "density": {"normal"}, "font": {"16px"}},
		{"mode": {"dark"}, "accent": {"teal"}, "density": {"normal"}}, // font missing
	} {
		if w := do(h, "POST", "/admin/theme/default", bad, c); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%v: %d", bad, w.Code)
		}
	}
	if got := settingValues(t, q); got["theme_mode"] != "" {
		t.Fatalf("saved despite errors: %v", got)
	}
	w := do(h, "POST", "/admin/theme/default", good, c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/customers" {
		t.Fatalf("save: %d %s", w.Code, w.Header().Get("Location"))
	}
	got := settingValues(t, q)
	if got["theme_mode"] != "dark" || got["theme_accent"] != "#12ab34" || got["theme_density"] != "roomy" || got["theme_font"] != "large" {
		t.Fatalf("stored %v", got)
	}
	// an open redirect through next is refused
	good.Set("next", "//evil.example")
	if w := do(h, "POST", "/admin/theme/default", good, c); w.Header().Get("Location") != "/admin" {
		t.Fatalf("next: %q", w.Header().Get("Location"))
	}
	// the defaults reach the admin pages and the portal
	for _, p := range []struct {
		path string
		c    *http.Cookie
	}{{"/admin", c}, {"/login", nil}, {"/portal/login", nil}} {
		b := do(h, "GET", p.path, nil, p.c).Body.String()
		for _, want := range []string{`data-def-mode="dark"`, `data-def-accent="#12ab34"`, `data-def-density="roomy"`, `data-def-font="large"`} {
			if !strings.Contains(b, want) {
				t.Fatalf("%s lacks %s", p.path, want)
			}
		}
	}
	// the popover button for "default for everyone" is SuperAdmin only
	if !strings.Contains(do(h, "GET", "/admin", nil, c).Body.String(), "/admin/theme/default") {
		t.Fatal("superadmin sees no default button")
	}
	if strings.Contains(do(h, "GET", "/admin", nil, login(t, h, "bob")).Body.String(), "/admin/theme/default") {
		t.Fatal("admin sees the default button")
	}
}

func TestThemeDefaultsFallBack(t *testing.T) {
	_, h, _ := settingsSetup(t)
	b := do(h, "GET", "/login", nil, nil).Body.String()
	for _, want := range []string{`data-def-mode="system"`, `data-def-accent="teal"`, `data-def-density="normal"`, `data-def-font="medium"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("missing %s", want)
		}
	}
}

func postLogos(t *testing.T, h http.Handler, c *http.Cookie, files ...string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("company_name", "Acme")
	mw.WriteField("currency_code", "Rp")
	for _, f := range files {
		fw, _ := mw.CreateFormFile(f, f+".png")
		fw.Write(uploadPNG)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "/admin/settings/app", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
}

func TestDarkLogoUploadAndFallback(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	// light only: no dark image is rendered, the light one is used in both modes
	postLogos(t, h, c, "logo")
	light := settingValues(t, q)["logo"]
	b := do(h, "GET", "/admin", nil, c).Body.String()
	if !strings.Contains(b, "/uploads/"+light) || strings.Contains(b, "logo-dark") {
		t.Fatal("light-only logo rendering is wrong")
	}
	// with a dark logo both are rendered; the CSS picks by the .dark class
	postLogos(t, h, c, "logo_dark")
	dark := settingValues(t, q)["logo_dark"]
	if !uploadName.MatchString(dark) || dark == light {
		t.Fatalf("dark name %q", dark)
	}
	b = do(h, "GET", "/admin", nil, c).Body.String()
	if !strings.Contains(b, `src="/uploads/`+light+`" alt="" class="logo-light`) || !strings.Contains(b, `src="/uploads/`+dark+`" alt="" class="logo-dark`) {
		t.Fatal("dark logo missing on admin")
	}
	// the login page falls back to the company logos when no login page logo is set
	if b := do(h, "GET", "/login", nil, nil).Body.String(); !strings.Contains(b, dark) {
		t.Fatal("login page lacks the dark logo")
	}
	// a login page logo without a dark variant has none, even though the company logo has one
	postLogos(t, h, c, "login_page_logo")
	loginLogo := settingValues(t, q)["login_page_logo"]
	b = do(h, "GET", "/login", nil, nil).Body.String()
	if !strings.Contains(b, loginLogo) || strings.Contains(b, "logo-dark") {
		t.Fatal("login page logo fallback is wrong")
	}
	if !strings.Contains(do(h, "GET", "/admin/settings/app", nil, c).Body.String(), `name="logo_dark"`) {
		t.Fatal("no dark logo field")
	}
}
