package web

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/secret"
)

const restorePageURL = "/admin/settings/miscellaneous/restore"

// restoreApp is the test app with its database file known, as main sets it.
func restoreApp(t *testing.T) (*Server, http.Handler, *http.Cookie) {
	t.Helper()
	s, _ := newTestApp(t)
	s.SecretKey = bytes.Repeat([]byte{7}, 32)
	s.BackupDir = t.TempDir()
	var seq int
	var name, file string
	if err := s.conn.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	s.DBPath = file
	h := s.Handler()
	return s, h, login(t, h, "alice")
}

// backupFile is a VACUUM INTO copy of the test database, edited by mutate before it is returned.
func backupFile(t *testing.T, s *Server, mutate func(*sql.DB)) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.db")
	if _, err := s.conn.Exec("VACUUM INTO ?", path); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		b, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		mutate(b)
		b.Close()
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// restoreUpload posts the backup file to the restore page.
func restoreUpload(t *testing.T, h http.Handler, c *http.Cookie, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("backup", "nuxbill.db")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(body)
	mw.Close()
	r := httptest.NewRequest("POST", restorePageURL, &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(c)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// stagedFiles lists the upload files next to the database.
func stagedFiles(t *testing.T, s *Server) []string {
	t.Helper()
	names, err := filepath.Glob(s.DBPath + ".upload-*")
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestRestoreIsSuperAdminOnly(t *testing.T) {
	s, h, _ := restoreApp(t)
	c := login(t, h, "rita")
	wantCode(t, do(h, "GET", restorePageURL, nil, c), 403, "page")
	wantCode(t, restoreUpload(t, h, c, backupFile(t, s, nil)), 403, "upload")
	wantCode(t, do(h, "POST", restorePageURL+"/confirm", url.Values{"token": {"x"}}, c), 403, "confirm")
}

func TestRestoreRefusesNonSQLite(t *testing.T) {
	s, h, c := restoreApp(t)
	w := restoreUpload(t, h, c, []byte("this is not a database, just text padding padding padding"))
	wantCode(t, w, 422, "text file")
	if !strings.Contains(w.Body.String(), "Bukan backup gobill") {
		t.Fatalf("no refusal text: %s", w.Body.String())
	}
	if n := len(stagedFiles(t, s)); n != 0 {
		t.Fatalf("refused upload left %d files", n)
	}
}

func TestRestoreCheckShowsCounts(t *testing.T) {
	s, h, c := restoreApp(t)
	body := backupFile(t, s, func(b *sql.DB) {
		if _, err := b.Exec("INSERT INTO customers (username, password_hash, fullname) VALUES ('budi', 'x', 'Budi')"); err != nil {
			t.Fatal(err)
		}
	})
	w := restoreUpload(t, h, c, body)
	wantCode(t, w, 200, "check")
	page := w.Body.String()
	if !importToken.MatchString(page) {
		t.Fatalf("no confirm token: %s", page)
	}
	if !strings.Contains(page, "<td>Pelanggan</td><td>1</td><td>0</td>") {
		t.Fatalf("customer counts missing: %s", page)
	}
	if n := len(stagedFiles(t, s)); n != 1 {
		t.Fatalf("want the staged upload, got %d files", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM customers"); n != 0 {
		t.Fatalf("check changed the live database: %d customers", n)
	}
}

func TestRestoreRefusesNewerVersion(t *testing.T) {
	s, h, c := restoreApp(t)
	body := backupFile(t, s, func(b *sql.DB) {
		if _, err := b.Exec("PRAGMA user_version = 999"); err != nil {
			t.Fatal(err)
		}
	})
	w := restoreUpload(t, h, c, body)
	wantCode(t, w, 422, "newer")
	if !strings.Contains(w.Body.String(), "Backup dari versi gobill yang lebih baru") {
		t.Fatalf("no newer-version refusal: %s", w.Body.String())
	}
}

func TestRestoreRefusesOtherKey(t *testing.T) {
	s, h, c := restoreApp(t)
	other := bytes.Repeat([]byte{9}, 32)
	sealed, err := secret.Seal(other, []byte("router-pass"))
	if err != nil {
		t.Fatal(err)
	}
	body := backupFile(t, s, func(b *sql.DB) {
		if _, err := b.Exec("INSERT INTO routers (name, host, username, password_enc) VALUES ('r1', '10.0.0.1', 'api', ?)", sealed); err != nil {
			t.Fatal(err)
		}
	})
	w := restoreUpload(t, h, c, body)
	wantCode(t, w, 422, "other key")
	if !strings.Contains(w.Body.String(), "Kunci enkripsi berbeda") {
		t.Fatalf("no key refusal: %s", w.Body.String())
	}
	if n := len(stagedFiles(t, s)); n != 0 {
		t.Fatalf("refused upload left %d files", n)
	}
}

func TestRestoreConfirmStagesBacksUpAndRestarts(t *testing.T) {
	s, h, c := restoreApp(t)
	restarts := 0
	s.Restart = func() { restarts++ }

	w := restoreUpload(t, h, c, backupFile(t, s, nil))
	wantCode(t, w, 200, "check")
	m := importToken.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatal("no token")
	}
	token := m[1]

	wantCode(t, do(h, "POST", restorePageURL+"/confirm", url.Values{"token": {token}}, c), 422, "no checkbox")
	if _, err := os.Stat(s.DBPath + ".restore"); !os.IsNotExist(err) {
		t.Fatal("refused confirm staged a restore")
	}

	w = do(h, "POST", restorePageURL+"/confirm", url.Values{"token": {token}, "understand": {"1"}}, c)
	wantCode(t, w, 200, "confirm")
	if !strings.Contains(w.Body.String(), "Pemulihan dijadwalkan") {
		t.Fatalf("no scheduled message: %s", w.Body.String())
	}
	if restarts != 1 {
		t.Fatalf("restart hook called %d times", restarts)
	}
	if _, err := os.Stat(s.DBPath + ".restore"); err != nil {
		t.Fatalf("no staged restore file: %v", err)
	}
	if n := len(stagedFiles(t, s)); n != 0 {
		t.Fatalf("confirm left %d upload files", n)
	}
	backups, _ := filepath.Glob(filepath.Join(s.BackupDir, "nuxbill-*-pre-import.db"))
	if len(backups) != 1 {
		t.Fatalf("want one automatic backup before the restore, got %v", backups)
	}
}
