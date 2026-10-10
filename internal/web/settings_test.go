package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

// settingsSetup adds an Admin (bob) next to the SuperAdmin alice and the Report rita.
func settingsSetup(t *testing.T) (*Server, http.Handler, *db.Queries) {
	s, q := newTestApp(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if _, err := q.CreateAdmin(t.Context(), db.CreateAdminParams{Username: "bob", Fullname: "Bob", PasswordHash: string(hash), Role: "Admin"}); err != nil {
		t.Fatal(err)
	}
	s.lang.Store("english") // assertions below match English messages
	return s, s.Handler(), q
}

func settingValues(t *testing.T, q *db.Queries) map[string]string {
	t.Helper()
	rows, err := q.ListSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m
}

func TestSettingsPagesRender(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	for _, tab := range []string{"app", "localisation", "notifications", "integrations", "miscellaneous"} {
		if w := do(h, "GET", "/admin/settings/"+tab, nil, c); w.Code != 200 {
			t.Fatalf("%s: %d", tab, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/settings", nil, c); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/settings/app" {
		t.Fatalf("bare settings: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/settings/nope", nil, c); w.Code != http.StatusNotFound {
		t.Fatalf("unknown tab: %d", w.Code)
	}
}

func TestSettingsSavePersists(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	form := url.Values{"company_name": {"Acme"}, "company_footer": {"Acme ISP"}, "address": {"Jl. Merdeka 1"},
		"phone": {"0812"}, "note": {"Thanks"}, "currency_code": {"Rp"}}
	w := do(h, "POST", "/admin/settings/app", form, c)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/settings/app" {
		t.Fatalf("save: %d", w.Code)
	}
	if got := settingValues(t, q); got["company_name"] != "Acme" || got["address"] != "Jl. Merdeka 1" {
		t.Fatalf("not persisted: %v", got)
	}
	w = do(h, "POST", "/admin/settings/localisation", url.Values{"language": {"english"}, "timezone": {"Asia/Makassar"},
		"date_format": {"d-m-Y"}, "country_code_phone": {"62"}}, c)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("localisation save: %d", w.Code)
	}
	w = do(h, "GET", "/admin/settings/localisation", nil, c)
	if !strings.Contains(w.Body.String(), "Asia/Makassar") || !strings.Contains(w.Body.String(), "Settings saved") {
		t.Fatalf("value or flash missing: %s", w.Body.String())
	}
}

func TestSettingsSecretNotRenderedAndKept(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	do(h, "POST", "/admin/settings/integrations", url.Values{"telegram_bot": {"123:SECRET"}, "telegram_target_id": {"42"}}, c)
	if w := do(h, "GET", "/admin/settings/integrations", nil, c); strings.Contains(w.Body.String(), "123:SECRET") {
		t.Fatal("secret rendered")
	}
	// an empty secret keeps the stored value; other fields still save
	do(h, "POST", "/admin/settings/integrations", url.Values{"telegram_bot": {""}, "telegram_target_id": {"43"}}, c)
	if got := settingValues(t, q); got["telegram_bot"] != "123:SECRET" || got["telegram_target_id"] != "43" {
		t.Fatalf("secret not kept: %v", got)
	}
}

func TestSettingsValidation422(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	cases := []struct {
		tab  string
		form url.Values
		want string
	}{
		{"localisation", url.Values{"language": {"english"}, "timezone": {"Mars/Base"}, "country_code_phone": {"62"}}, "Unknown timezone"},
		{"integrations", url.Values{"wa_url": {"http://gw/send?to=x"}}, "URL must contain"},
		{"integrations", url.Values{"wa_url": {"http://gw/send?to=[number]"}}, "URL must contain"},
		{"integrations", url.Values{"webhook_url": {"ftp://x.test/hook"}}, "http or https"},
		{"integrations", url.Values{"smtp_port": {"70000"}}, "Enter a port"},
		{"notifications", url.Values{"reminder_hour": {"24"}}, "Enter a whole number"},
	}
	for _, tc := range cases {
		w := do(h, "POST", "/admin/settings/"+tc.tab, tc.form, c)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s %v: got %d", tc.tab, tc.form, w.Code)
		}
		if tc.tab == "localisation" && !strings.Contains(w.Body.String(), "Mars/Base") {
			t.Fatal("entered timezone not kept")
		}
	}
	got := settingValues(t, q)
	delete(got, "app_url") // stored by the admin login itself, not the form
	if len(got) != 0 {
		t.Fatalf("saved despite errors: %v", got)
	}
}

func TestSettingsAdminRole(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "bob")
	for _, tab := range []string{"app", "localisation", "notifications", "miscellaneous"} {
		if w := do(h, "GET", "/admin/settings/"+tab, nil, c); w.Code != 200 {
			t.Fatalf("admin %s: %d", tab, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/settings/integrations", nil, c); w.Code != http.StatusForbidden {
		t.Fatalf("admin integrations: %d", w.Code)
	}
	if w := do(h, "POST", "/admin/settings/integrations", url.Values{"telegram_target_id": {"1"}}, c); w.Code != http.StatusForbidden {
		t.Fatalf("admin integrations post: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/settings/app", nil, login(t, h, "rita")); w.Code != http.StatusForbidden {
		t.Fatalf("report role: %d", w.Code)
	}
}

func TestSettingsChangedCalledOnSave(t *testing.T) {
	s, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	calls := 0
	s.SettingsChanged = func(context.Context) { calls++ }
	do(h, "POST", "/admin/settings/app", url.Values{"company_name": {""}, "currency_code": {"Rp"}}, c)
	if calls != 0 {
		t.Fatal("hook called on a rejected save")
	}
	do(h, "POST", "/admin/settings/app", url.Values{"company_name": {"Acme"}, "currency_code": {"Rp"}}, c)
	if calls != 1 {
		t.Fatalf("hook calls: %d", calls)
	}
}

func TestSettingsCheckboxStoresYesNo(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	if w := do(h, "POST", "/admin/settings/miscellaneous", url.Values{"man_fields_email": {"1"}, "enable_balance": {"yes"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", w.Code)
	}
	got := settingValues(t, q)
	if got["man_fields_email"] != "yes" || got["man_fields_fname"] != "no" || got["maintenance_mode"] != "no" {
		t.Fatalf("checkboxes not yes/no: %v", got)
	}
	if w := do(h, "GET", "/admin/settings/miscellaneous", nil, c); !strings.Contains(w.Body.String(), `checked`) {
		t.Fatal("saved checkbox not rendered as checked")
	}
}

func TestSettingsNewFieldsPersist(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	saves := []struct {
		tab  string
		form url.Values
	}{
		{"app", url.Values{"company_name": {"Acme"}, "currency_code": {"Rp"}, "login_page_head": {"Welcome"},
			"login_page_description": {"Log in here"}}},
		{"localisation", url.Values{"language": {"english"}, "timezone": {"Asia/Jakarta"}, "dec_point": {","},
			"thousands_sep": {"'"}, "reset_day": {"15"}}},
		{"notifications", url.Values{"reminder_hour": {"7"}, "notif_invoice_balance": {"bal"}, "notif_welcome_message": {"hi"},
			"notif_balance_send": {"sent"}, "notif_balance_received": {"got"}}},
		{"integrations", url.Values{"mail_reply_to": {"help@acme.test"}}},
		{"miscellaneous", url.Values{"voucher_format": {"numbers"}, "disable_registration": {"yes"},
			"registration_username": {"phone"}, "sms_otp_registration": {"yes"}, "phone_otp_type": {"wa"},
			"reg_nofify_admin": {"yes"}, "session_timeout_duration": {"30"}, "single_session": {"yes"},
			"maintenance_date": {"2026-10-31"}}},
	}
	for _, sv := range saves {
		if w := do(h, "POST", "/admin/settings/"+sv.tab, sv.form, c); w.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d %s", sv.tab, w.Code, w.Body.String())
		}
	}
	got := settingValues(t, q)
	for k, want := range map[string]string{"login_page_head": "Welcome", "login_page_description": "Log in here",
		"dec_point": ",", "thousands_sep": "'", "reset_day": "15", "notif_invoice_balance": "bal",
		"notif_welcome_message": "hi", "notif_balance_send": "sent", "notif_balance_received": "got",
		"mail_reply_to": "help@acme.test", "voucher_format": "numbers", "disable_registration": "yes",
		"registration_username": "phone", "sms_otp_registration": "yes", "phone_otp_type": "wa",
		"reg_nofify_admin": "yes", "session_timeout_duration": "30", "single_session": "yes",
		"maintenance_date": "2026-10-31"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	bad := []struct {
		tab  string
		form url.Values
	}{
		{"localisation", url.Values{"language": {"english"}, "timezone": {"Asia/Jakarta"}, "reset_day": {"29"}}},
		{"miscellaneous", url.Values{"session_timeout_duration": {"0"}}},
		{"miscellaneous", url.Values{"phone_otp_type": {"email"}}},
		{"miscellaneous", url.Values{"maintenance_date": {"31/10/2026"}}},
	}
	for _, tc := range bad {
		if w := do(h, "POST", "/admin/settings/"+tc.tab, tc.form, c); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%v: %d", tc.form, w.Code)
		}
	}
}

// uploadPNG is a PNG signature plus padding; DetectContentType sniffs only the signature.
var uploadPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func postLogo(t *testing.T, h http.Handler, c *http.Cookie, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("company_name", "Acme")
	mw.WriteField("currency_code", "Rp")
	fw, _ := mw.CreateFormFile("logo", "logo.png")
	fw.Write(data)
	mw.Close()
	r := httptest.NewRequest("POST", "/admin/settings/app", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSettingsUploadRules(t *testing.T) {
	s, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	for _, data := range [][]byte{[]byte("hello"), svg} {
		if w := postLogo(t, h, c, data); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Use a PNG") {
			t.Fatalf("non-image accepted: %d", w.Code)
		}
	}
	if w := postLogo(t, h, c, append(uploadPNG, make([]byte, maxUpload)...)); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "2 MB") {
		t.Fatalf("oversize accepted: %d", w.Code)
	}
	if got := settingValues(t, q)["logo"]; got != "" {
		t.Fatalf("rejected upload stored: %q", got)
	}
	if w := postLogo(t, h, c, uploadPNG); w.Code != http.StatusSeeOther {
		t.Fatalf("png: %d %s", w.Code, w.Body.String())
	}
	name := settingValues(t, q)["logo"]
	if !uploadName.MatchString(name) {
		t.Fatalf("stored name %q", name)
	}
	dir, err := s.uploadDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	// a save without a new file keeps the stored image
	do(h, "POST", "/admin/settings/app", url.Values{"company_name": {"Acme"}, "currency_code": {"Rp"}}, c)
	if got := settingValues(t, q)["logo"]; got != name {
		t.Fatalf("logo lost: %q", got)
	}
}

func TestUploadsServeRejectsTraversal(t *testing.T) {
	s, _, _ := settingsSetup(t)
	dir, err := s.uploadDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("a", 32) + ".png"
	if err := os.WriteFile(filepath.Join(dir, name), uploadPNG, 0o640); err != nil {
		t.Fatal(err)
	}
	serve := func(n string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/uploads/"+n, nil)
		r.SetPathValue("name", n)
		w := httptest.NewRecorder()
		s.serveUpload(w, r)
		return w
	}
	for _, bad := range []string{"../" + name, "..%2F" + name, "sub/" + name, "../gobill.db", name + "/.."} {
		if w := serve(bad); w.Code != http.StatusNotFound {
			t.Fatalf("%q served: %d", bad, w.Code)
		}
	}
	if w := serve(name); w.Code != http.StatusOK || !bytes.HasPrefix(w.Body.Bytes(), uploadPNG[:8]) {
		t.Fatalf("valid upload: %d", w.Code)
	}
}

func TestDBBackupSuperAdminOnly(t *testing.T) {
	s, h, _ := settingsSetup(t)
	backup := s.sessions.LoadAndSave(s.requireAdmin("SuperAdmin", "Admin")(http.HandlerFunc(s.dbBackup)))
	w := do(backup, "GET", "/backup", nil, login(t, h, "alice"))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "SQLite format 3\x00") {
		t.Fatalf("superadmin: %d", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("not an attachment: %q", w.Header().Get("Content-Disposition"))
	}
	if w := do(backup, "GET", "/backup", nil, login(t, h, "bob")); w.Code != http.StatusForbidden {
		t.Fatalf("admin: %d", w.Code)
	}
}

func TestMoneyUsesThousandsSep(t *testing.T) {
	_, h, _ := settingsSetup(t)
	t.Cleanup(func() { setThousandsSep(".") })
	if got := money(1234000); got != "Rp 1.234.000" {
		t.Fatalf("default: %q", got)
	}
	do(h, "POST", "/admin/settings/localisation", url.Values{"language": {"english"}, "timezone": {"Asia/Jakarta"},
		"thousands_sep": {","}}, login(t, h, "alice"))
	if got := money(1234000); got != "Rp 1,234,000" {
		t.Fatalf("thousands_sep: %q", got)
	}
}

func TestBrandingAndBackupRoutesReachable(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	if w := postLogo(t, h, c, uploadPNG); w.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d", w.Code)
	}
	name := settingValues(t, q)["logo"]
	if w := do(h, "GET", "/uploads/"+name, nil, nil); w.Code != http.StatusOK || !bytes.HasPrefix(w.Body.Bytes(), uploadPNG[:8]) {
		t.Fatalf("/uploads: %d", w.Code)
	}
	if w := do(h, "GET", "/uploads/../gobill.db", nil, nil); w.Code == http.StatusOK {
		t.Fatal("traversal served")
	}
	do(h, "POST", "/admin/settings/app", url.Values{"company_name": {"Acme"}, "currency_code": {"Rp"},
		"login_page_head": {"Acme Head"}, "login_page_description": {"Log in here"}}, c)
	if w := do(h, "GET", "/login", nil, nil); !strings.Contains(w.Body.String(), "Acme Head") || !strings.Contains(w.Body.String(), "Log in here") || !strings.Contains(w.Body.String(), "/uploads/"+name) {
		t.Fatal("branding missing on login page")
	}
	if w := do(h, "GET", "/admin/settings/miscellaneous/backup", nil, c); w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "SQLite format 3\x00") {
		t.Fatalf("backup as SuperAdmin: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/settings/miscellaneous/backup", nil, login(t, h, "bob")); w.Code != http.StatusForbidden {
		t.Fatalf("backup as Admin: %d", w.Code)
	}
	if w := do(h, "GET", "/admin/settings/miscellaneous", nil, c); !strings.Contains(w.Body.String(), "/admin/settings/miscellaneous/backup") {
		t.Fatal("backup button missing")
	}
	if w := do(h, "GET", "/admin/settings/app", nil, c); !strings.Contains(w.Body.String(), `enctype="multipart/form-data"`) {
		t.Fatal("multipart form missing on the upload page")
	}
}

func TestSettingsPortalOptions(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	form := url.Values{"disable_voucher": {"yes"}, "voucher_redirect": {"https://192.168.88.1/status"},
		"show_bandwidth_plan": {"yes"}, "extend_expired": {"1"}, "extend_days": {"3"}, "extend_confirmation": {"I agree"},
		"allow_balance_transfer": {"yes"}, "minimum_transfer": {"5000"}, "allow_balance_custom": {"yes"},
		"allow_phone_otp": {"yes"}, "allow_email_otp": {"no"}, "hs_auth_method": {"chap"}, "maintenance_mode_logout": {"1"}}
	if w := do(h, "POST", "/admin/settings/miscellaneous", form, c); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	got := settingValues(t, q)
	for k, want := range map[string]string{"disable_voucher": "yes", "voucher_redirect": "https://192.168.88.1/status",
		"show_bandwidth_plan": "yes", "extend_expired": "1", "extend_days": "3", "extend_confirmation": "I agree",
		"allow_balance_transfer": "yes", "minimum_transfer": "5000", "allow_balance_custom": "yes",
		"allow_phone_otp": "yes", "allow_email_otp": "no", "hs_auth_method": "chap", "maintenance_mode_logout": "yes"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if w := do(h, "GET", "/admin/settings/miscellaneous", nil, c); !strings.Contains(w.Body.String(), "https://192.168.88.1/status") {
		t.Fatal("saved voucher_redirect not rendered")
	}
	bad := []url.Values{
		{"voucher_redirect": {"javascript:alert(1)"}},
		{"voucher_redirect": {"ftp://192.168.88.1/x"}},
		{"voucher_redirect": {"https://"}},
		{"extend_days": {"-1"}},
		{"extend_days": {"three"}},
		{"minimum_transfer": {"-5000"}},
		{"hs_auth_method": {"api"}},
	}
	for _, f := range bad {
		if w := do(h, "POST", "/admin/settings/miscellaneous", f, c); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%v: %d", f, w.Code)
		}
	}
}

func TestSettingsDefaultPlanDevice(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	if w := do(h, "POST", "/admin/settings/miscellaneous", url.Values{"default_plan_device": {"Radius"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if got := settingValues(t, q)["default_plan_device"]; got != "Radius" {
		t.Fatalf("stored %q", got)
	}
	if w := do(h, "POST", "/admin/settings/miscellaneous", url.Values{"default_plan_device": {"Bogus"}}, c); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad value: %d", w.Code)
	}
	if got := settingValues(t, q)["default_plan_device"]; got != "Radius" {
		t.Fatalf("bad value was stored: %q", got)
	}
}

// TestSettingsGeneralHiddenKeysKept: company_footer and currency_code have no consumer in gobill, so
// the General form no longer shows them. Their stored values (e.g. imported from PHPNuxBill) survive a save.
func TestSettingsGeneralHiddenKeysKept(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	for _, kv := range [][2]string{{"company_footer", "Legacy footer"}, {"currency_code", "IDR"}} {
		if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: kv[0], Value: kv[1]}); err != nil {
			t.Fatal(err)
		}
	}
	body := getBody(t, h, c, "/admin/settings/app")
	if strings.Contains(body, `name="company_footer"`) || strings.Contains(body, `name="currency_code"`) {
		t.Fatal("hidden no-consumer field still rendered")
	}
	if w := do(h, "POST", "/admin/settings/app", url.Values{"company_name": {"Acme"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if got := settingValues(t, q); got["company_footer"] != "Legacy footer" || got["currency_code"] != "IDR" {
		t.Fatalf("hidden keys changed: %v", got)
	}
}

// Two images near the 2 MB cap in one save: each file is within its limit, so the body must be accepted.
func TestSettingsTwoLargeUploads(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	big := append(append([]byte{}, uploadPNG...), make([]byte, 3*maxUpload/4)...)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("company_name", "Acme")
	for _, f := range []string{"logo", "login_page_wallpaper"} {
		fw, _ := mw.CreateFormFile(f, f+".png")
		fw.Write(big)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "/admin/settings/app", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("two large images refused: %d", w.Code)
	}
	if v := settingValues(t, q); v["logo"] == "" || v["login_page_wallpaper"] == "" {
		t.Fatalf("not stored: %q %q", v["logo"], v["login_page_wallpaper"])
	}
}

// The stored static QRIS is redrawn on the payment tab so the admin can check it.
func TestSettingsQRISPreview(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	if strings.Contains(do(h, "GET", "/admin/settings/payment", nil, c).Body.String(), "data:image/png;base64") {
		t.Fatal("preview without a stored QRIS")
	}
	payload := "00020101021126610014COM.GO-JEK.WWW01189360091431538383250210G1538383250303UMI51440014ID.CO.QRIS.WWW0215ID10264879603990303UMI5204481453033605802ID59164 Keys Solutions6010YOGYAKARTA61055516162140703A0111036216304BA80"
	if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "qris_payload", Value: payload}); err != nil {
		t.Fatal(err)
	}
	if b := do(h, "GET", "/admin/settings/payment", nil, c).Body.String(); !strings.Contains(b, "data:image/png;base64") {
		t.Fatal("no QRIS preview")
	}
}
