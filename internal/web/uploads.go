package web

// Serving of the stored upload images.

import (
	"net/http"
	"path/filepath"

	"github.com/frand-kod/gobill/internal/web/core"
)

// serveUpload serves a stored image. The name must match core.UploadName, so no path can leave the folder.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !core.UploadName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	dir, err := s.ax.UploadDir()
	if err != nil {
		s.Fail(w, "uploads dir", err)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filepath.Join(dir, name))
}
