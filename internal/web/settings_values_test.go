package web

import (
	"bytes"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/notify"
)

// settingSample holds a valid value for fields whose generic sample would not pass validation.
var settingSample = map[string]string{
	"company_name": "Acme Net", "currency_code": "Rp", "timezone": "Asia/Makassar", "reminder_hour": "8",
	"reset_day": "5", "session_timeout_duration": "30", "daily_summary_time": "07:30", "maintenance_date": "2026-10-31",
	"sms_url": "http://gw.test/send?to=[number]&text=[text]", "wa_url": "http://gw.test/wa?to=[number]&text=[text]",
	"alt_wga_server_url": "http://127.0.0.1:3030", "smtp_port": "587", "webhook_url": "https://hook.test/x",
	"voucher_redirect": "https://192.168.88.1/status", "extend_days": "3", "minimum_transfer": "5000",
	"language":     "english",
	"qris_payload": "00020101021126610014COM.GO-JEK.WWW01189360091431538383250210G1538383250303UMI51440014ID.CO.QRIS.WWW0215ID10264879603990303UMI5204481453033605802ID59164 Keys Solutions6010YOGYAKARTA61055516162140703A0111036216304BA80",
}

// settingSampleFor picks the value a test saves into a non-secret field: the last real choice of a
// select (so it differs from the first option), or a per-type sample.
func settingSampleFor(f field) string {
	if v, ok := settingSample[f.Name]; ok {
		return v
	}
	switch f.Type {
	case "select":
		for i := len(f.Options) - 1; i >= 0; i-- {
			if f.Options[i].Value != "" {
				return f.Options[i].Value
			}
		}
	case "textarea":
		return "Line one & <b>two</b>"
	case "number":
		return "7"
	case "time":
		return "08:15"
	}
	return "v-" + f.Name
}

// secretHint returns the hint under a password field, so each secret is checked on its own.
func secretHint(body, name string) string {
	m := regexp.MustCompile(`(?s)name="` + name + `" type="password".*?<p class="hint">([^<]*)</p>`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// selectBlock returns the <select> whose name is name, so option markup is checked inside it only.
func selectBlock(body, name string) string {
	return regexp.MustCompile(`(?s)<select[^>]*name="` + regexp.QuoteMeta(name) + `".*?</select>`).FindString(body)
}

func getBody(t *testing.T, h http.Handler, c *http.Cookie, path string) string {
	t.Helper()
	w := do(h, "GET", path, nil, c)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", path, w.Code)
	}
	return w.Body.String()
}

// TestSettingsEveryFieldShowsStoredValue saves a value for every non-secret field of every tab and
// asserts the page renders it back: value attribute, selected option, checked box or textarea text.
func TestSettingsEveryFieldShowsStoredValue(t *testing.T) {
	s, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	for _, tab := range settingsTabs {
		fields := s.settingsFields(tab.Slug, nil, nil, nil)
		form, want, checked := url.Values{}, map[string]string{}, map[string]bool{}
		for i, f := range fields {
			switch f.Type {
			case "watest", "dsnow", "formbtn", "link", "password", "file", "hidden":
				continue
			case "checkbox":
				checked[f.Name] = i%2 == 0 // a mix of ticked and unticked
				if checked[f.Name] {
					form.Set(f.Name, "1")
				}
			default:
				want[f.Name] = settingSampleFor(f)
				form.Set(f.Name, want[f.Name])
			}
		}
		if w := do(h, "POST", "/admin/settings/"+tab.Slug, form, c); w.Code != http.StatusSeeOther {
			t.Fatalf("%s: save %d: %s", tab.Slug, w.Code, w.Body.String())
		}
		body := getBody(t, h, c, "/admin/settings/"+tab.Slug)
		for _, f := range fields {
			switch f.Type {
			case "watest", "dsnow", "formbtn", "password", "file", "hidden":
				continue
			case "checkbox":
				m := regexp.MustCompile(`name="` + f.Name + `" value="1"( checked)?>`).FindStringSubmatch(body)
				if m == nil {
					t.Errorf("%s/%s: checkbox missing", tab.Slug, f.Name)
				} else if (m[1] == " checked") != checked[f.Name] {
					t.Errorf("%s/%s: checked=%v, want %v", tab.Slug, f.Name, !checked[f.Name], checked[f.Name])
				}
			case "select":
				if !strings.Contains(selectBlock(body, f.Name), `<option value="`+html.EscapeString(want[f.Name])+`" selected>`) {
					t.Errorf("%s/%s: option %q not selected", tab.Slug, f.Name, want[f.Name])
				}
			case "textarea":
				m := regexp.MustCompile(`(?s)name="` + f.Name + `" rows="4"[^>]*>(.*?)</textarea>`).FindStringSubmatch(body)
				if m == nil || html.UnescapeString(m[1]) != want[f.Name] {
					t.Errorf("%s/%s: textarea does not show %q", tab.Slug, f.Name, want[f.Name])
				}
			case "qris": // the advanced paste box next to the photo upload
				if !strings.Contains(body, `name="`+f.Name+`" value="`+html.EscapeString(want[f.Name])+`"`) {
					t.Errorf("%s/%s: value %q not shown", tab.Slug, f.Name, want[f.Name])
				}
			default: // text, number, time, date
				if !strings.Contains(body, `name="`+f.Name+`" type="`+f.Type+`" value="`+html.EscapeString(want[f.Name])+`"`) {
					t.Errorf("%s/%s: value %q not shown", tab.Slug, f.Name, want[f.Name])
				}
			}
		}
	}
}

// postSettingsMultipart posts a settings form with image files, as the browser does.
func postSettingsMultipart(t *testing.T, h http.Handler, c *http.Cookie, tab string, fields map[string]string, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	for k, data := range files {
		fw, _ := mw.CreateFormFile(k, k+".png")
		fw.Write(data)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "/admin/settings/"+tab, &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestSettingsImagePreviewReplaceRemove covers the image fields: the stored file is shown with a
// preview and a remove box, a new upload replaces it, a remove deletes it, and a failed save keeps it.
func TestSettingsImagePreviewReplaceRemove(t *testing.T) {
	s, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	dir, err := s.uploadDir()
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"company_name": "Acme", "currency_code": "Rp"}
	png2 := append(append([]byte{}, uploadPNG...), 'x') // a different file, so a different stored name

	if w := postSettingsMultipart(t, h, c, "app", base, map[string][]byte{"logo": uploadPNG}); w.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	first := settingValues(t, q)["logo"]
	body := getBody(t, h, c, "/admin/settings/app")
	if !strings.Contains(body, `<img src="/uploads/`+first+`"`) || !strings.Contains(body, `<span class="min-w-0 break-all text-sm">`+first+`</span>`) || !strings.Contains(body, `name="logo_remove"`) {
		t.Fatalf("preview, name or remove box missing for stored logo")
	}

	// a second logo field shows its own preview too
	if w := postSettingsMultipart(t, h, c, "app", base, map[string][]byte{"login_page_favicon": png2}); w.Code != http.StatusSeeOther {
		t.Fatalf("favicon upload: %d", w.Code)
	}
	fav := settingValues(t, q)["login_page_favicon"]
	if body := getBody(t, h, c, "/admin/settings/app"); !strings.Contains(body, `<img src="/uploads/`+fav+`"`) || !strings.Contains(body, `<img src="/uploads/`+first+`"`) {
		t.Fatal("favicon or logo preview missing")
	}

	// a new upload replaces the old file, which is deleted from disk
	if w := postSettingsMultipart(t, h, c, "app", base, map[string][]byte{"logo": png2}); w.Code != http.StatusSeeOther {
		t.Fatalf("replace: %d", w.Code)
	}
	second := settingValues(t, q)["logo"]
	if second == first || !uploadName.MatchString(second) {
		t.Fatalf("logo not replaced: %q", second)
	}
	if _, err := os.Stat(filepath.Join(dir, first)); !os.IsNotExist(err) {
		t.Fatalf("replaced image still on disk: %v", err)
	}

	// a validation error keeps the stored image on the re-rendered page
	bad := map[string]string{"company_name": "", "currency_code": "Rp"}
	if w := postSettingsMultipart(t, h, c, "app", bad, nil); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `<img src="/uploads/`+second+`"`) {
		t.Fatalf("422 lost the image preview: %d", w.Code)
	}

	// remove clears the setting and deletes the file
	if w := do(h, "POST", "/admin/settings/app", url.Values{"company_name": {"Acme"}, "currency_code": {"Rp"}, "logo_remove": {"1"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", w.Code)
	}
	if got := settingValues(t, q)["logo"]; got != "" {
		t.Fatalf("logo not cleared: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, second)); !os.IsNotExist(err) {
		t.Fatalf("removed image still on disk: %v", err)
	}
	if body := getBody(t, h, c, "/admin/settings/app"); strings.Contains(body, `name="logo_remove"`) {
		t.Fatal("remove box still shown after removing")
	}
}

// TestSettingsSecretsMarkedSavedNotEchoed: every secret is never rendered, shows a saved hint once
// set, and keeps the hint when a save fails.
func TestSettingsSecretsMarkedSavedNotEchoed(t *testing.T) {
	s, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	const saved = "Saved. Leave empty to keep the current secret"
	errField := map[string][2]string{"integrations": {"smtp_port", "70000"}, "payment": {"tripay_mode", "bogus"}}
	for _, tab := range settingsTabs {
		for _, f := range s.settingsFields(tab.Slug, nil, nil, nil) {
			if f.Type != "password" {
				continue
			}
			secret := "S3CRET-" + f.Name
			body := getBody(t, h, c, "/admin/settings/"+tab.Slug)
			if secretHint(body, f.Name) != "Leave empty to keep the current secret" {
				t.Fatalf("%s: unsaved secret hint %q", f.Name, secretHint(body, f.Name))
			}
			if w := do(h, "POST", "/admin/settings/"+tab.Slug, url.Values{f.Name: {secret}}, c); w.Code != http.StatusSeeOther {
				t.Fatalf("%s: save %d", f.Name, w.Code)
			}
			body = getBody(t, h, c, "/admin/settings/"+tab.Slug)
			if strings.Contains(body, secret) || secretHint(body, f.Name) != saved {
				t.Fatalf("%s: saved secret echoed or not marked saved", f.Name)
			}
			// a failed save keeps the hint and never echoes what was typed
			form := url.Values{f.Name: {"NEW-" + secret}, errField[tab.Slug][0]: {errField[tab.Slug][1]}}
			w := do(h, "POST", "/admin/settings/"+tab.Slug, form, c)
			if w.Code != http.StatusUnprocessableEntity || secretHint(w.Body.String(), f.Name) != saved || strings.Contains(w.Body.String(), "NEW-"+secret) {
				t.Fatalf("%s: 422 %d shows the typed secret or drops the hint", f.Name, w.Code)
			}
		}
	}
}

// TestSettingsKeepsTypedValuesOn422: a validation error re-renders what the user typed, and an
// empty template clears instead of falling back to the built-in text.
func TestSettingsKeepsTypedValuesOn422(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	form := url.Values{"voucher_redirect": {"https://typed.test/x"}, "extend_confirmation": {"Typed & kept"},
		"voucher_format": {"numbers"}, "man_fields_email": {"1"}, "minimum_transfer": {"5000"}, "session_timeout_duration": {"abc"}}
	w := do(h, "POST", "/admin/settings/miscellaneous", form, c)
	body := w.Body.String()
	for _, want := range []string{`value="https://typed.test/x"`, `Typed &amp; kept`, `<option value="numbers" selected>`,
		`name="man_fields_email" value="1" checked>`, `name="minimum_transfer" type="number" value="5000"`} {
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(body, want) {
			t.Fatalf("422 lost %s (%d)", want, w.Code)
		}
	}

	def := notify.DefaultTemplate("expired")
	if def == "" {
		t.Fatal("no built-in expired template")
	}
	w = do(h, "POST", "/admin/settings/notifications", url.Values{"notif_expired": {""}, "reminder_hour": {"99"}}, c)
	if w.Code != http.StatusUnprocessableEntity || strings.Contains(w.Body.String(), html.EscapeString(def)) {
		t.Fatalf("cleared template refilled with the built-in text: %d", w.Code)
	}

	// an unsaved daily summary channel shows Disabled, not the first real channel
	if block := selectBlock(getBody(t, h, c, "/admin/settings/notifications"), "daily_summary_channel"); !strings.Contains(block, `<option value="" selected>`) {
		t.Fatalf("empty channel not shown as Disabled: %s", block)
	}
}
