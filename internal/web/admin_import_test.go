package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// importFixture is a small PHPNuxBill backup: one customer and one admin with a sha1 password.
const importFixture = `{
	"tbl_appconfig": [{"id":"1","setting":"timezone","value":"Asia/Jakarta"}],
	"tbl_users": [{"id":"1","username":"pakadmin","fullname":"Pak Admin","password":"0123456789abcdef0123456789abcdef01234567",
		"phone":null,"email":null,"city":null,"user_type":"Admin","status":"Active","root":"0","last_login":null,"creationdate":"2024-01-01 00:00:00"}],
	"tbl_customers": [{"id":"1","username":"budi","password":"pw1","fullname":"Budi","address":null,"phonenumber":"0812345678",
		"email":"budi@example.com","balance":"2500.00","service_type":"PPPoE","pppoe_username":"budi","pppoe_password":"ppw",
		"pppoe_ip":null,"auto_renewal":"0","status":"Active","created_by":"0","created_at":"2024-01-01 00:00:00","last_login":null}]
}`

var importToken = regexp.MustCompile(`name="token" value="([0-9a-f]{32})"`)

func importApp(t *testing.T) (*Server, http.Handler, *http.Cookie) {
	s, _ := newTestApp(t)
	s.SecretKey = bytes.Repeat([]byte{7}, 32)
	s.BackupDir = t.TempDir()
	h := s.Handler()
	return s, h, login(t, h, "alice")
}

// importUpload posts the multipart form of the import page; files maps the field name to its content.
func importUpload(t *testing.T, h http.Handler, c *http.Cookie, files map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, err := mw.CreateFormFile(name, name+".json")
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(content))
	}
	mw.Close()
	r := httptest.NewRequest("POST", importPage, &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// preview uploads the fixture and returns the confirm token shown on the preview.
func preview(t *testing.T, h http.Handler, c *http.Cookie) string {
	t.Helper()
	w := importUpload(t, h, c, map[string]string{"backup": importFixture})
	wantCode(t, w, 200, "preview")
	m := importToken.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no token on preview: %s", w.Body.String())
	}
	return m[1]
}

// tempImports is the set of upload files in the temp dir, to see what a test left behind.
func tempImports(t *testing.T) map[string]bool {
	names, err := filepath.Glob(filepath.Join(os.TempDir(), importPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return set
}

func newImports(t *testing.T, before map[string]bool) []string {
	var out []string
	for n := range tempImports(t) {
		if !before[n] {
			out = append(out, n)
		}
	}
	return out
}

func countRows(t *testing.T, s *Server, q string) int {
	t.Helper()
	var n int
	if err := s.conn.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportIsSuperAdminOnly(t *testing.T) {
	_, h, _ := importApp(t)
	c := login(t, h, "rita")
	wantCode(t, do(h, "GET", importPage, nil, c), 403, "page")
	wantCode(t, importUpload(t, h, c, map[string]string{"backup": importFixture}), 403, "preview")
	wantCode(t, do(h, "POST", importPage+"/confirm", url.Values{"token": {"x"}}, c), 403, "confirm")
}

func TestImportPreviewShowsCountsAndChangesNothing(t *testing.T) {
	s, h, c := importApp(t)
	before := tempImports(t)
	w := importUpload(t, h, c, map[string]string{"backup": importFixture})
	wantCode(t, w, 200, "preview")
	body := w.Body.String()
	if !strings.Contains(body, "<td>customers</td><td>1</td><td>1</td><td>0</td>") {
		t.Fatalf("customer row missing from preview: %s", body)
	}
	if !strings.Contains(body, "Data yang ada di aplikasi ini: 0 Pelanggan") {
		t.Fatal("current counts missing from preview")
	}
	if n := countRows(t, s, "SELECT count(*) FROM admins"); n != 2 {
		t.Fatalf("preview changed admins: %d", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM customers"); n != 0 {
		t.Fatalf("preview imported customers: %d", n)
	}
	if len(newImports(t, before)) != 1 {
		t.Fatal("preview should keep one upload file for the confirm")
	}
	// the preview's upload is removed by the cancel, and the next preview replaces it
	wantCode(t, do(h, "POST", importPage+"/cancel", nil, c), 303, "cancel")
	if left := newImports(t, before); len(left) != 0 {
		t.Fatalf("cancel left files: %v", left)
	}
}

func TestImportConfirmNeedsCurrentToken(t *testing.T) {
	s, h, c := importApp(t)
	stale := preview(t, h, c)
	current := preview(t, h, c) // the second preview replaces the first token

	wantCode(t, do(h, "POST", importPage+"/confirm", url.Values{"understand": {"1"}}, c), 422, "no token")
	wantCode(t, do(h, "POST", importPage+"/confirm", url.Values{"token": {stale}, "understand": {"1"}}, c), 422, "stale token")
	wantCode(t, do(h, "POST", importPage+"/confirm", url.Values{"token": {current}}, c), 422, "no confirm box")
	if n := countRows(t, s, "SELECT count(*) FROM customers"); n != 0 {
		t.Fatalf("refused confirm still imported: %d customers", n)
	}
}

func TestImportConfirmBacksUpReplacesAndEndsSession(t *testing.T) {
	s, h, c := importApp(t)
	before := tempImports(t)
	token := preview(t, h, c)

	w := do(h, "POST", importPage+"/confirm", url.Values{"token": {token}, "understand": {"1"}}, c)
	wantCode(t, w, 303, "confirm")
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Fatalf("redirect to %q", loc)
	}
	backups, _ := filepath.Glob(filepath.Join(s.BackupDir, "nuxbill-*-pre-import.db"))
	if len(backups) != 1 {
		t.Fatalf("want one pre-import backup, got %v", backups)
	}
	if n := countRows(t, s, "SELECT count(*) FROM customers WHERE username = 'budi'"); n != 1 {
		t.Fatalf("customer not imported: %d", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM admins WHERE username = 'alice'"); n != 0 {
		t.Fatal("old admin survived the import")
	}
	if left := newImports(t, before); len(left) != 0 {
		t.Fatalf("confirm left files: %v", left)
	}
	// the old session is gone; the redirect sets a fresh session that carries the flash to the login page
	if w := do(h, "GET", "/admin", nil, c); w.Header().Get("Location") != "/login" {
		t.Fatalf("old session still works: %d %q", w.Code, w.Header().Get("Location"))
	}
	var fresh *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "nuxbill_session" {
			fresh = ck
		}
	}
	if fresh == nil {
		t.Fatal("confirm sets no session cookie")
	}
	if body := do(h, "GET", "/login", nil, fresh).Body.String(); !strings.Contains(body, "Impor selesai") {
		t.Fatal("login page has no import flash")
	}
}
