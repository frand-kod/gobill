package settings

// Restore a gobill backup (a .db copy) from Settings > System & Security (SuperAdmin only). The upload is
// staged next to the live database and checked read-only. Confirm backs up the current data, moves the
// staged file to <db>.restore and restarts the app. db.ApplyPendingRestore swaps it in at startup.

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/service/restore"
	"github.com/frand-kod/gobill/internal/web/core"
)

const restorePage = "/admin/settings/system/restore"

// restoreView is the page data. Token, Backup and Newest are set after a check; Done after confirm.
type restoreView struct {
	Token   string
	Size    int64
	Backup  importCounts
	Current importCounts
	Newest  string
	Warning string // key fingerprint differs, shown translated by renderRestore
	Done    bool
	TOTP    bool // the confirm asks for a 2FA code too
}

// restorePath is the staged upload: the live database file name plus ".upload-<random>".
func (s *Handlers) sweepRestores() {
	names, _ := filepath.Glob(s.DBPath + ".upload-*")
	for _, p := range names {
		if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) > importTTL {
			os.Remove(p)
		}
	}
}

// dropRestore deletes the staged upload and forgets it.
func (s *Handlers) dropRestore(ctx context.Context) {
	if p := s.Sessions.GetString(ctx, "restore_file"); p != "" {
		os.Remove(p)
	}
	s.Sessions.Remove(ctx, "restore_file")
	s.Sessions.Remove(ctx, "restore_token")
}

// pendingRestore returns the staged file when token is the one this session was given and the file is fresh.
func (s *Handlers) pendingRestore(ctx context.Context, token string) (string, bool) {
	want := s.Sessions.GetString(ctx, "restore_token")
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(token)) != 1 {
		return "", false
	}
	p := s.Sessions.GetString(ctx, "restore_file")
	if fi, err := os.Stat(p); p == "" || err != nil || time.Since(fi.ModTime()) > importTTL {
		return "", false
	}
	return p, true
}

// saveRestoreUpload streams the "backup" part to a new file next to the database. It returns the
// file name even on error, so the caller can delete it.
func (s *Handlers) saveRestoreUpload(r *http.Request) (string, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", err
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if p.FormName() != "backup" || p.FileName() == "" {
			continue // an empty file input sends no file
		}
		f, err := os.CreateTemp(filepath.Dir(s.DBPath), filepath.Base(s.DBPath)+".upload-*")
		if err != nil {
			return "", err
		}
		_, err = io.Copy(f, p)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return f.Name(), err
	}
}

func (s *Handlers) renderRestore(w http.ResponseWriter, r *http.Request, status int, v restoreView, msg, detail string) {
	v.TOTP = core.AdminFrom(r).TotpEnabled == 1
	if app, bak, ok := strings.Cut(v.Warning, " "); ok { // Warning holds the two key fingerprints
		v.Warning = fmt.Sprintf(s.T("This backup was made with another encryption key. It holds no encrypted values now, but you need that key to enter router passwords and API keys again. Key fingerprint of this app: %s. Key fingerprint of this backup: %s."), app, bak)
	}
	p := core.Page{Title: "Restore database", Data: v, Detail: detail}
	if msg != "" {
		p.Error = s.T(msg)
	}
	s.Render(w, r, status, "restore", p)
}

// restoreAgain re-renders the preview of the staged file with an error (wrong password, missing tick),
// so the admin does not have to upload again. If the preview cannot be rebuilt the plain form is shown.
func (s *Handlers) restoreAgain(w http.ResponseWriter, r *http.Request, token, path, msg string) {
	v := restoreView{}
	ctx := r.Context()
	bak, newest, warn, err := restore.Inspect(ctx, path, s.SecretKey, s.Location())
	cur, err2 := restore.CountRows(ctx, s.Conn)
	fi, err3 := os.Stat(path)
	if err == nil && err2 == nil && err3 == nil {
		v = restoreView{Token: token, Size: fi.Size(), Backup: bak, Current: cur, Newest: newest, Warning: warn}
	}
	s.renderRestore(w, r, http.StatusUnprocessableEntity, v, msg, "")
}

func (s *Handlers) restoreForm(w http.ResponseWriter, r *http.Request) {
	s.sweepRestores()
	s.renderRestore(w, r, http.StatusOK, restoreView{}, "", "")
}

// restorePreview stages the upload and checks it. Nothing is changed until the confirm.
func (s *Handlers) restorePreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.sweepRestores()
	s.dropRestore(ctx)
	extendDeadlines(w)
	r.Body = http.MaxBytesReader(w, r.Body, importMaxBody)
	path, err := s.saveRestoreUpload(r)
	if err != nil {
		if path != "" {
			os.Remove(path)
		}
		slog.Warn("restore upload", "err", err)
		s.renderRestore(w, r, http.StatusUnprocessableEntity, restoreView{}, "Upload failed. The file may be larger than 200 MB.", "")
		return
	}
	if path == "" {
		s.renderRestore(w, r, http.StatusUnprocessableEntity, restoreView{}, "Choose the backup file (.db).", "")
		return
	}
	fail := func(err error) {
		os.Remove(path)
		var rf *restore.Refusal
		if errors.As(err, &rf) {
			s.renderRestore(w, r, http.StatusUnprocessableEntity, restoreView{}, rf.Msg, rf.Detail)
			return
		}
		s.Fail(w, "restore check", err)
	}
	token, err := newToken()
	if err != nil {
		fail(err)
		return
	}
	cur, err := restore.CountRows(ctx, s.Conn)
	if err != nil {
		fail(err)
		return
	}
	bak, newest, warn, err := restore.Inspect(ctx, path, s.SecretKey, s.Location())
	if err != nil {
		fail(err)
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		fail(err)
		return
	}
	s.Sessions.Put(ctx, "restore_token", token)
	s.Sessions.Put(ctx, "restore_file", path)
	s.renderRestore(w, r, http.StatusOK, restoreView{Token: token, Size: fi.Size(), Backup: bak, Current: cur, Newest: newest, Warning: warn}, "", "")
}

// restoreConfirm backs up the current data, stages the file as <db>.restore and asks main to restart.
func (s *Handlers) restoreConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.sweepRestores()
	extendDeadlines(w)
	path, ok := s.pendingRestore(ctx, r.PostFormValue("token"))
	if !ok {
		s.renderRestore(w, r, http.StatusUnprocessableEntity, restoreView{}, "The restore file has expired. Upload it again.", "")
		return
	}
	if r.PostFormValue("understand") != "1" {
		s.restoreAgain(w, r, r.PostFormValue("token"), path, "Tick the box to confirm the overwrite.")
		return
	}
	// replacing the whole database is as sensitive as the key download: ask for the password again
	if msg, err := s.stepUp(r); err != nil {
		s.Fail(w, "open totp secret", err)
		return
	} else if msg != "" {
		s.restoreAgain(w, r, r.PostFormValue("token"), path, msg)
		return
	}
	bak, err := s.importBackup(ctx, "pre-restore")
	if err != nil {
		s.dropRestore(ctx)
		slog.Error("restore backup", "err", err)
		s.renderRestore(w, r, http.StatusInternalServerError, restoreView{}, "Backup failed. Nothing was restored.", "")
		return
	}
	if err := os.Rename(path, s.DBPath+".restore"); err != nil {
		s.dropRestore(ctx)
		s.Fail(w, "restore stage", err)
		return
	}
	s.dropRestore(ctx) // the file is now staged under its new name; only the session keys go
	slog.Warn("restore scheduled, restarting", "backup", bak, "db", s.DBPath)
	s.renderRestore(w, r, http.StatusOK, restoreView{Done: true}, "", "")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if s.Restart != nil {
		s.Restart()
	}
}

func (s *Handlers) restoreCancel(w http.ResponseWriter, r *http.Request) {
	s.dropRestore(r.Context())
	s.Sessions.Put(r.Context(), "flash", s.T("Restore cancelled."))
	http.Redirect(w, r, restorePage, http.StatusSeeOther)
}

// SweepUploads is the job body that removes abandoned import and restore uploads, so they do not
// stay on disk until someone happens to visit those pages.
func (s *Handlers) SweepUploads(context.Context) error {
	sweepImports()
	s.sweepRestores()
	return nil
}
