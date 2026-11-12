package adminx

// Uploaded files: storage, validation and serving.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"

	"errors"
	"io"
	"os"

	"github.com/frand-kod/gobill/internal/web/core"
)

const MaxUpload = 2 << 20

// UploadExt maps the sniffed types to extensions. SVG sniffs as text, so it is refused (XSS).
var UploadExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/x-icon": ".ico"}

// UploadDir is <db dir>/uploads, found from the open database file so no other setting is needed.
func (s *Base) UploadDir() (string, error) {
	var file string
	if err := s.Conn.QueryRow("SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(file), "uploads"), nil
}

// StoreUpload checks the sniffed type and size, then saves the image under a random name.
func (s *Base) StoreUpload(fh *multipart.FileHeader) (string, error) {
	if fh.Size > MaxUpload {
		return "", errors.New("File must be 2 MB or less")
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	ext, ok := UploadExt[http.DetectContentType(head[:n])]
	if !ok {
		return "", errors.New("Use a PNG, JPG, WebP or ICO image")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	dir, err := s.UploadDir()
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
	if _, err := io.Copy(out, io.LimitReader(f, MaxUpload)); err != nil {
		return "", err
	}
	return name, out.Close()
}

// RemoveUnused deletes stored images that no setting points to any more (replaced or removed).
// Only names in the upload format are touched, so nothing outside the uploads folder can go.
func (s *Base) RemoveUnused(ctx context.Context, names []string) {
	if len(names) == 0 {
		return
	}
	st, err := s.LoadSettings(ctx)
	if err != nil {
		slog.Error("remove unused uploads", "err", err)
		return
	}
	used := map[string]bool{}
	for _, x := range st {
		used[x] = true
	}
	dir, err := s.UploadDir()
	if err != nil {
		slog.Error("remove unused uploads", "err", err)
		return
	}
	for _, n := range names {
		if !core.UploadName.MatchString(n) || used[n] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Error("remove unused upload", "name", n, "err", err)
		}
	}
}
