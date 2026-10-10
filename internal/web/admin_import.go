package web

// Import a PHPNuxBill JSON backup from Settings > Miscellaneous (SuperAdmin only). The upload is
// streamed to temp files and kept under a random session token until the operator confirms,
// cancels or 30 minutes pass. Confirm makes a database backup first, then replaces the data.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/frand-kod/gobill/internal/importer"
)

const (
	importMaxBody = 200 << 20 // a PHPNuxBill backup can be large; only the import routes accept this
	importTTL     = 30 * time.Minute
	importPrefix  = "nuxbill-import-"
	importPage    = "/admin/settings/miscellaneous/import"
)

// importCounts are the rows gobill holds now, which an import replaces.
type importCounts struct{ Customers, Plans, Subscriptions, Transactions, Admins int64 }

func (c importCounts) filled() bool {
	return c.Customers+c.Plans+c.Subscriptions+c.Transactions+c.Admins > 0
}

// importRow is one table of the preview. Reasons holds at most 10 skips, More the rest.
type importRow struct {
	Name                  string
	Read, Loaded, Skipped int
	Reasons               []string
	More                  int
	Notes                 []string
}

// importView is the page data. Token and Rows are set after a preview.
type importView struct {
	Token   string
	Rows    []importRow
	Current importCounts
	Filled  bool // gobill has data: confirm asks first
}

func countImportRows(ctx context.Context, conn *sql.DB) (importCounts, error) {
	var c importCounts
	for _, t := range []struct {
		table string
		n     *int64
	}{{"customers", &c.Customers}, {"plans", &c.Plans}, {"subscriptions", &c.Subscriptions}, {"transactions", &c.Transactions}, {"admins", &c.Admins}} {
		if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM "+t.table).Scan(t.n); err != nil {
			return c, err
		}
	}
	return c, nil
}

// sweepImports deletes uploads left in the temp dir for longer than importTTL.
func sweepImports() {
	names, _ := filepath.Glob(filepath.Join(os.TempDir(), importPrefix+"*"))
	for _, p := range names {
		if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) > importTTL {
			os.Remove(p)
		}
	}
}

// dropImport deletes the pending upload files and forgets them.
func (s *Server) dropImport(ctx context.Context) {
	for _, k := range []string{"import_json", "import_notif"} {
		if p := s.sessions.GetString(ctx, k); p != "" {
			os.Remove(p)
		}
		s.sessions.Remove(ctx, k)
	}
	s.sessions.Remove(ctx, "import_token")
}

// pendingImport returns the upload files when token is the one this session was given and the files are still fresh.
func (s *Server) pendingImport(ctx context.Context, token string) (map[string]string, bool) {
	want := s.sessions.GetString(ctx, "import_token")
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(token)) != 1 {
		return nil, false
	}
	files := map[string]string{"backup": s.sessions.GetString(ctx, "import_json"), "notifications": s.sessions.GetString(ctx, "import_notif")}
	for _, p := range files {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err != nil || time.Since(fi.ModTime()) > importTTL {
			return nil, false
		}
	}
	return files, files["backup"] != ""
}

// saveUpload streams the "backup" and "notifications" parts to temp files. It returns the files
// it wrote even on error, so the caller can delete them.
func saveUpload(r *http.Request) (map[string]string, error) {
	files := map[string]string{}
	mr, err := r.MultipartReader()
	if err != nil {
		return files, err
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return files, err
		}
		name := p.FormName()
		if (name != "backup" && name != "notifications") || p.FileName() == "" {
			continue // an empty file input sends no file
		}
		f, err := os.CreateTemp(os.TempDir(), importPrefix+"*.json")
		if err != nil {
			return files, err
		}
		files[name] = f.Name()
		_, err = io.Copy(f, p)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return files, err
		}
	}
}

func removeFiles(files map[string]string) {
	for _, p := range files {
		os.Remove(p)
	}
}

// runImport reads the uploaded backup and imports it into the app database. dry rolls everything back.
func (s *Server) runImport(ctx context.Context, files map[string]string, dry bool) (*importer.Report, error) {
	src, err := importer.OpenJSON(ctx, files["backup"])
	if err != nil {
		return nil, err
	}
	defer src.Close()
	loc, err := time.LoadLocation(importer.Timezone(ctx, src))
	if err != nil {
		return nil, err
	}
	return importer.Run(ctx, src, s.conn, importer.Options{Key: s.SecretKey, Loc: loc, DryRun: dry, Force: true, Notifications: files["notifications"]})
}

// importBackup writes a VACUUM INTO copy of the database to BackupDir and returns its file name.
// The name does not match the daily backup pattern, so the daily rotation never deletes it.
func (s *Server) importBackup(ctx context.Context) (string, error) {
	if err := os.MkdirAll(s.BackupDir, 0o700); err != nil {
		return "", err
	}
	name := "nuxbill-" + time.Now().Format("20060102-150405") + "-pre-import.db"
	final := filepath.Join(s.BackupDir, name)
	tmp := final + ".tmp"
	os.Remove(tmp)
	if _, err := s.conn.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// extendDeadlines lifts the server's 15 s read and 30 s write timeouts for these routes: a large
// backup takes longer to upload and to import. Writers that do not support it (tests) just ignore it.
func extendDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	t := time.Now().Add(10 * time.Minute)
	rc.SetReadDeadline(t)
	rc.SetWriteDeadline(t)
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func importRows(rep *importer.Report) []importRow {
	var out []importRow
	for _, t := range rep.Tables {
		row := importRow{Name: t.Name, Read: t.Read, Loaded: t.Loaded, Skipped: len(t.Skips), Reasons: t.Skips, Notes: t.Notes}
		if len(row.Reasons) > 10 {
			row.More = len(row.Reasons) - 10
			row.Reasons = row.Reasons[:10]
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) renderImport(w http.ResponseWriter, r *http.Request, status int, v importView, msg, detail string) {
	p := Page{Title: "Import PHPNuxBill", Data: v, Detail: detail}
	if msg != "" {
		p.Error = s.catalog.T(s.language(), msg)
	}
	s.render(w, r, status, "import", p)
}

func (s *Server) importForm(w http.ResponseWriter, r *http.Request) {
	sweepImports()
	s.renderImport(w, r, http.StatusOK, importView{}, "", "")
}

// importPreview stores the upload and runs the import as a dry run, so the operator sees what would happen.
func (s *Server) importPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sweepImports()
	s.dropImport(ctx)
	extendDeadlines(w)
	r.Body = http.MaxBytesReader(w, r.Body, importMaxBody)
	files, err := saveUpload(r)
	if err != nil {
		removeFiles(files)
		slog.Warn("import upload", "err", err)
		s.renderImport(w, r, http.StatusUnprocessableEntity, importView{}, "Upload failed. The file may be larger than 200 MB.", "")
		return
	}
	if files["backup"] == "" {
		s.renderImport(w, r, http.StatusUnprocessableEntity, importView{}, "Choose the backup file (.json).", "")
		return
	}
	token, err := newToken()
	if err != nil {
		removeFiles(files)
		s.fail(w, "import token", err)
		return
	}
	rep, err := s.runImport(ctx, files, true)
	if err != nil {
		removeFiles(files)
		s.renderImport(w, r, http.StatusUnprocessableEntity, importView{}, "Could not read this file as a PHPNuxBill backup.", err.Error())
		return
	}
	cur, err := countImportRows(ctx, s.conn)
	if err != nil {
		removeFiles(files)
		s.fail(w, "import counts", err)
		return
	}
	s.sessions.Put(ctx, "import_token", token)
	s.sessions.Put(ctx, "import_json", files["backup"])
	s.sessions.Put(ctx, "import_notif", files["notifications"])
	s.renderImport(w, r, http.StatusOK, importView{Token: token, Rows: importRows(rep), Current: cur, Filled: cur.filled()}, "", "")
}

// importConfirm backs up the database, imports for real, then ends the session: the admins were replaced.
func (s *Server) importConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sweepImports()
	extendDeadlines(w)
	files, ok := s.pendingImport(ctx, r.PostFormValue("token"))
	if !ok {
		s.renderImport(w, r, http.StatusUnprocessableEntity, importView{}, "The import file has expired. Upload it again.", "")
		return
	}
	cur, err := countImportRows(ctx, s.conn)
	if err != nil {
		s.fail(w, "import counts", err)
		return
	}
	if cur.filled() && r.PostFormValue("understand") != "1" {
		s.renderImport(w, r, http.StatusUnprocessableEntity, importView{}, "Tick the box to confirm the overwrite.", "")
		return
	}
	bak, err := s.importBackup(ctx)
	if err != nil {
		s.dropImport(ctx)
		slog.Error("import backup", "err", err)
		s.renderImport(w, r, http.StatusInternalServerError, importView{}, "Backup failed. Nothing was imported.", "")
		return
	}
	rep, err := s.runImport(ctx, files, false)
	s.dropImport(ctx)
	if err != nil {
		slog.Error("import", "err", err)
		s.renderImport(w, r, http.StatusUnprocessableEntity, importView{}, "Import failed. Nothing was changed.", err.Error())
		return
	}
	loaded, skipped := 0, 0
	for _, t := range rep.Tables {
		loaded += t.Loaded
		skipped += len(t.Skips)
	}
	slog.Info("phpnuxbill import", "backup", bak, "loaded", loaded, "skipped", skipped)

	// the import replaced settings and admins: reload what is cached, then end this session
	if m, err := s.loadSettings(ctx); err != nil {
		slog.Error("import reload settings", "err", err)
	} else {
		s.lang.Store(m["language"])
	}
	s.ReloadSessionSettings(ctx)
	if s.SettingsChanged != nil {
		s.SettingsChanged(ctx)
	}
	lang := s.language()
	msg := s.catalog.T(lang, "Import finished. Log in with a PHPNuxBill admin account.") + " " +
		fmt.Sprintf(s.catalog.T(lang, "%d rows imported, %d skipped"), loaded, skipped) + ". " +
		fmt.Sprintf(s.catalog.T(lang, "Backup saved as %s"), bak)
	s.sessions.Destroy(ctx)
	s.sessions.Put(ctx, "flash", msg)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) importCancel(w http.ResponseWriter, r *http.Request) {
	s.dropImport(r.Context())
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), "Import cancelled."))
	http.Redirect(w, r, importPage, http.StatusSeeOther)
}
