package web

// Import a PHPNuxBill JSON backup from Settings > System & Security (SuperAdmin only). The upload is
// streamed to temp files and kept under a random session token until the operator confirms,
// cancels or 30 minutes pass. Confirm makes a database backup first, then replaces the data.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/frand-kod/gobill/internal/service/restore"
	"github.com/frand-kod/gobill/internal/settings"
	"github.com/frand-kod/gobill/internal/web/core"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/importer"
)

const (
	importMaxBody = 200 << 20 // a PHPNuxBill backup can be large; only the import routes accept this
	importTTL     = 30 * time.Minute
	importPrefix  = "gobill-import-"
	importPage    = "/admin/settings/system/import"
)

// importCounts are the rows gobill holds now, which an import replaces.
type importCounts = restore.Counts

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
	TOTP    bool // the confirm asks for a 2FA code too
	// Unsupported lists the PHPNuxBill data and settings gobill does not import or use.
	Unsupported []string
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
		if p := s.Sessions.GetString(ctx, k); p != "" {
			os.Remove(p)
		}
		s.Sessions.Remove(ctx, k)
	}
	s.Sessions.Remove(ctx, "import_token")
}

// pendingImport returns the upload files when token is the one this session was given and the files are still fresh.
func (s *Server) pendingImport(ctx context.Context, token string) (map[string]string, bool) {
	want := s.Sessions.GetString(ctx, "import_token")
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(token)) != 1 {
		return nil, false
	}
	files := map[string]string{"backup": s.Sessions.GetString(ctx, "import_json"), "notifications": s.Sessions.GetString(ctx, "import_notif")}
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

// importBackup writes a VACUUM INTO copy of the database to BackupDir, named with label (pre-import or pre-restore), and returns its file name.
// The name does not match the daily backup pattern, so the daily rotation never deletes it.
func (s *Server) importBackup(ctx context.Context, label string) (string, error) {
	return db.Snapshot(ctx, s.conn, s.BackupDir, label)
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
	v.TOTP = adminFrom(r).TotpEnabled == 1
	p := Page{Title: "Import PHPNuxBill", Data: v, Detail: detail}
	if msg != "" {
		p.Error = s.T(msg)
	}
	s.Render(w, r, status, "import", p)
}

// importAgain re-renders the dry-run preview of the staged file with an error (wrong password, missing
// tick), so the admin does not have to upload again. If the preview cannot be rebuilt the plain form is shown.
func (s *Server) importAgain(w http.ResponseWriter, r *http.Request, token string, files map[string]string, cur importCounts, msg string) {
	v := importView{}
	if rep, err := s.runImport(r.Context(), files, true); err == nil {
		v = importView{Token: token, Rows: importRows(rep), Current: cur, Filled: cur.Filled(), Unsupported: rep.Unsupported}
	}
	s.renderImport(w, r, http.StatusUnprocessableEntity, v, msg, "")
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
		s.Fail(w, "import token", err)
		return
	}
	rep, err := s.runImport(ctx, files, true)
	if err != nil {
		removeFiles(files)
		s.renderImport(w, r, http.StatusUnprocessableEntity, importView{}, "Could not read this file as a PHPNuxBill backup.", err.Error())
		return
	}
	cur, err := restore.CountRows(ctx, s.conn)
	if err != nil {
		removeFiles(files)
		s.Fail(w, "import counts", err)
		return
	}
	s.Sessions.Put(ctx, "import_token", token)
	s.Sessions.Put(ctx, "import_json", files["backup"])
	s.Sessions.Put(ctx, "import_notif", files["notifications"])
	s.renderImport(w, r, http.StatusOK, importView{Token: token, Rows: importRows(rep), Current: cur, Filled: cur.Filled(), Unsupported: rep.Unsupported}, "", "")
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
	cur, err := restore.CountRows(ctx, s.conn)
	if err != nil {
		s.Fail(w, "import counts", err)
		return
	}
	if cur.Filled() && r.PostFormValue("understand") != "1" {
		s.importAgain(w, r, r.PostFormValue("token"), files, cur, "Tick the box to confirm the overwrite.")
		return
	}
	// the import replaces the admins and settings: ask for the password again, like the key download
	if msg, err := s.stepUp(r); err != nil {
		s.Fail(w, "open totp secret", err)
		return
	} else if msg != "" {
		s.importAgain(w, r, r.PostFormValue("token"), files, cur, msg)
		return
	}
	bak, err := s.importBackup(ctx, "pre-import")
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
	if m, err := s.LoadSettings(ctx); err != nil {
		slog.Error("import reload settings", "err", err)
	} else {
		s.Lang.Store(m["language"])
	}
	s.ReloadSessionSettings(ctx)
	s.settingsSaved(ctx)
	lang := s.Language()
	msg := s.Catalog.T(lang, "Import finished. Log in with a PHPNuxBill admin account.") + " " +
		fmt.Sprintf(s.Catalog.T(lang, "%d rows imported, %d skipped"), loaded, skipped) + ". " +
		fmt.Sprintf(s.Catalog.T(lang, "Backup saved as %s"), bak)
	// Quick Setup switches to verifying the imported data, and the first-run dialog does not come back
	if err := s.Queries.UpsertSetting(ctx, db.UpsertSettingParams{Key: setupModeKey, Value: "import"}); err != nil {
		slog.Error("import setup mode", "err", err)
	}
	if cur := settings.Get(ctx, s.Queries, core.SetupKey); cur == "" {
		if err := s.Queries.UpsertSetting(ctx, db.UpsertSettingParams{Key: core.SetupKey, Value: "started"}); err != nil {
			slog.Error("import setup state", "err", err)
		}
	}
	s.Sessions.Destroy(ctx)
	s.Sessions.Put(ctx, "flash", msg)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (s *Server) importCancel(w http.ResponseWriter, r *http.Request) {
	s.dropImport(r.Context())
	s.Sessions.Put(r.Context(), "flash", s.T("Import cancelled."))
	http.Redirect(w, r, importPage, http.StatusSeeOther)
}
