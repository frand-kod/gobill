package web

// Database backup download; the copy is made by service/restore.

import (
	"github.com/frand-kod/gobill/internal/service/restore"
	"net/http"

	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

// backupStatus is the one-line status under "Download backup", written by job.Backup. The mirror path is SuperAdmin only.
func (s *Server) backupStatus(st map[string]string) string {
	out := s.t("Last local backup: none yet")
	if st["backup_last_at"] != "" {
		out = fmt.Sprintf(s.t("Last local backup: %s %s"), st["backup_last_at"], st["backup_last_file"])
	}
	switch {
	case s.BackupMirror == "":
		return fmt.Sprintf(s.t("%s. Off-site mirror: off (GOBILL_BACKUP_MIRROR)"), out)
	case st["backup_mirror_error"] != "":
		return fmt.Sprintf(s.t("%s. Mirror %s ERROR: %s"), out, s.BackupMirror, st["backup_mirror_error"])
	case st["backup_mirror_at"] != "":
		return fmt.Sprintf(s.t("%s. Mirror %s, last copy %s"), out, s.BackupMirror, st["backup_mirror_at"])
	}
	return fmt.Sprintf(s.t("%s. Mirror %s, no copy yet"), out, s.BackupMirror)
}

// dbBackup streams a consistent copy of the database made with VACUUM INTO. SuperAdmin only.
func (s *Server) dbBackup(w http.ResponseWriter, r *http.Request) {
	if a := adminFrom(r); a == nil || a.Role != "SuperAdmin" {
		s.deny(w, r, http.StatusForbidden)
		return
	}
	copyPath, cleanup, err := restore.Backup(r.Context(), s.conn, s.DBPath)
	if err != nil {
		s.fail(w, "backup", err)
		return
	}
	defer cleanup()
	f, err := os.Open(copyPath)
	if err != nil {
		s.fail(w, "open backup", err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.fail(w, "stat backup", err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="gobill-%s.db"`, time.Now().Format("20060102-150405")))
	io.Copy(w, f)
}
