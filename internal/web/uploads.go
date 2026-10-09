package web

// Uploaded files: storage, validation and serving.

import (
	"crypto/rand"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"path/filepath"

	"errors"
	"io"
	"os"
	"regexp"
)

const maxUpload = 2 << 20

// uploadExt maps the sniffed types to extensions. SVG sniffs as text, so it is refused (XSS).
var uploadExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/x-icon": ".ico"}

var uploadName = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|webp|ico)$`)

// uploadDir is <db dir>/uploads, found from the open database file so no other setting is needed.
func (s *Server) uploadDir() (string, error) {
	var file string
	if err := s.conn.QueryRow("SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(file), "uploads"), nil
}

// storeUpload checks the sniffed type and size, then saves the image under a random name.
func (s *Server) storeUpload(fh *multipart.FileHeader) (string, error) {
	if fh.Size > maxUpload {
		return "", errors.New("File must be 2 MB or less")
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	ext, ok := uploadExt[http.DetectContentType(head[:n])]
	if !ok {
		return "", errors.New("Use a PNG, JPG, WebP or ICO image")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	dir, err := s.uploadDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	name := hex.EncodeToString(raw) + ext
	out, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, io.LimitReader(f, maxUpload)); err != nil {
		return "", err
	}
	return name, out.Close()
}

// serveUpload serves a stored image. The name must match uploadName, so no path can leave the folder.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !uploadName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	dir, err := s.uploadDir()
	if err != nil {
		s.fail(w, "uploads dir", err)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filepath.Join(dir, name))
}
