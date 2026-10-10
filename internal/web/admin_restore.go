package web

// Restore a gobill backup (a .db copy) from Settings > Miscellaneous (SuperAdmin only). The upload is
// staged next to the live database and checked read-only. Confirm backs up the current data, moves the
// staged file to <db>.restore and restarts the app. db.ApplyPendingRestore swaps it in at startup.

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
)

const restorePage = "/admin/settings/miscellaneous/restore"

// restoreView is the page data. Token, Backup and Newest are set after a check; Done after confirm.
type restoreView struct {
	Token   string
	Size    int64
	Backup  importCounts
	Current importCounts
	Newest  string
	Done    bool
}

// refusal is a file the restore page turns down. Msg is shown translated, Detail as is.
type refusal struct{ msg, detail string }

func (e *refusal) Error() string { return e.msg }

func notBackup(err error) error {
	return &refusal{msg: "Not a gobill backup: the file is not a readable SQLite database.", detail: err.Error()}
}

// restorePath is the staged upload: the live database file name plus ".upload-<random>".
func (s *Server) sweepRestores() {
	names, _ := filepath.Glob(s.DBPath + ".upload-*")
	for _, p := range names {
		if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) > importTTL {
			os.Remove(p)
		}
	}
}

// dropRestore deletes the staged upload and forgets it.
func (s *Server) dropRestore(ctx context.Context) {
	if p := s.sessions.GetString(ctx, "restore_file"); p != "" {
		os.Remove(p)
	}
	s.sessions.Remove(ctx, "restore_file")
	s.sessions.Remove(ctx, "restore_token")
}

// pendingRestore returns the staged file when token is the one this session was given and the file is fresh.
func (s *Server) pendingRestore(ctx context.Context, token string) (string, bool) {
	want := s.sessions.GetString(ctx, "restore_token")
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(token)) != 1 {
		return "", false
	}
	p := s.sessions.GetString(ctx, "restore_file")
	if fi, err := os.Stat(p); p == "" || err != nil || time.Since(fi.ModTime()) > importTTL {
		return "", false
	}
	return p, true
}

// saveRestoreUpload streams the "backup" part to a new file next to the database. It returns the
// file name even on error, so the caller can delete it.
func (s *Server) saveRestoreUpload(r *http.Request) (string, error) {
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

// inspectBackup opens the file read-only and checks that gobill can restore it. It returns a
// *refusal for a file it turns down.
func inspectBackup(ctx context.Context, path string, key []byte, loc *time.Location) (importCounts, string, error) {
	ro, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return importCounts{}, "", notBackup(err)
	}
	defer ro.Close()
	var check string
	if err := ro.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return importCounts{}, "", notBackup(err)
	}
	if check != "ok" {
		return importCounts{}, "", notBackup(errors.New(check))
	}
	var ver int
	if err := ro.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return importCounts{}, "", notBackup(err)
	}
	if ver == 0 {
		return importCounts{}, "", notBackup(errors.New("no migrations applied"))
	}
	latest, err := db.LatestVersion()
	if err != nil {
		return importCounts{}, "", err
	}
	if ver > latest {
		return importCounts{}, "", &refusal{msg: "Backup from a newer gobill version", detail: fmt.Sprintf("backup schema %d, this binary %d", ver, latest)}
	}
	counts, err := countImportRows(ctx, ro)
	if err != nil {
		return importCounts{}, "", notBackup(err)
	}
	// the sealed columns are encrypted with NUXBILL_SECRET_KEY: one non-empty value tells whether the key matches
	for _, q := range []string{
		"SELECT password_enc FROM routers WHERE length(password_enc) > 0 LIMIT 1",
		"SELECT secret_enc FROM nas WHERE length(secret_enc) > 0 LIMIT 1",
		"SELECT secret_enc FROM customers WHERE length(secret_enc) > 0 LIMIT 1",
	} {
		var sealed []byte
		err := ro.QueryRowContext(ctx, q).Scan(&sealed)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return importCounts{}, "", notBackup(err)
		}
		if _, err := secret.Open(key, sealed); err != nil {
			return importCounts{}, "", &refusal{msg: "Different encryption key: this backup was made with another NUXBILL_SECRET_KEY"}
		}
		break
	}
	var newest int64
	if err := ro.QueryRowContext(ctx, "SELECT coalesce(max(created_at), 0) FROM transactions").Scan(&newest); err != nil {
		return importCounts{}, "", notBackup(err)
	}
	if newest == 0 {
		return counts, "", nil
	}
	return counts, time.Unix(newest, 0).In(loc).Format("2006-01-02 15:04"), nil
}

func (s *Server) renderRestore(w http.ResponseWriter, r *http.Request, status int, v restoreView, msg, detail string) {
	p := Page{Title: "Restore database", Data: v, Detail: detail}
	if msg != "" {
		p.Error = s.catalog.T(s.language(), msg)
	}
	s.render(w, r, status, "restore", p)
}

func (s *Server) restoreForm(w http.ResponseWriter, r *http.Request) {
	s.sweepRestores()
	s.renderRestore(w, r, http.StatusOK, restoreView{}, "", "")
}

// restorePreview stages the upload and checks it. Nothing is changed until the confirm.
func (s *Server) restorePreview(w http.ResponseWriter, r *http.Request) {
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
		var rf *refusal
		if errors.As(err, &rf) {
			s.renderRestore(w, r, http.StatusUnprocessableEntity, restoreView{}, rf.msg, rf.detail)
			return
		}
		s.fail(w, "restore check", err)
	}
	token, err := newToken()
	if err != nil {
		fail(err)
		return
	}
	cur, err := countImportRows(ctx, s.conn)
	if err != nil {
		fail(err)
		return
	}
	bak, newest, err := inspectBackup(ctx, path, s.SecretKey, s.location())
	if err != nil {
		fail(err)
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		fail(err)
		return
	}
	s.sessions.Put(ctx, "restore_token", token)
	s.sessions.Put(ctx, "restore_file", path)
	s.renderRestore(w, r, http.StatusOK, restoreView{Token: token, Size: fi.Size(), Backup: bak, Current: cur, Newest: newest}, "", "")
}

// restoreConfirm backs up the current data, stages the file as <db>.restore and asks main to restart.
func (s *Server) restoreConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.sweepRestores()
	extendDeadlines(w)
	path, ok := s.pendingRestore(ctx, r.PostFormValue("token"))
	if !ok {
		s.renderRestore(w, r, http.StatusUnprocessableEntity, restoreView{}, "The restore file has expired. Upload it again.", "")
		return
	}
	if r.PostFormValue("understand") != "1" {
		s.renderRestore(w, r, http.StatusUnprocessableEntity, restoreView{}, "Tick the box to confirm the overwrite.", "")
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
		s.fail(w, "restore stage", err)
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

func (s *Server) restoreCancel(w http.ResponseWriter, r *http.Request) {
	s.dropRestore(r.Context())
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), "Restore cancelled."))
	http.Redirect(w, r, restorePage, http.StatusSeeOther)
}
