package web

// Versioned static URLs and cache headers. Templates write {{asset "/static/app.css"}}, which adds a
// version token, so a new build never gets an old stylesheet from a browser or Cloudflare cache.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"

	gobill "github.com/frand-kod/gobill"
)

type staticIndex struct {
	etags map[string]string // path under web/static -> quoted ETag (content hash)
	hash  string            // short hash of all files, the token for dev builds
}

var (
	staticOnce sync.Once
	staticIdx  staticIndex
)

// staticFiles hashes the embedded web/static tree once. The files never change while the process runs.
func staticFiles() staticIndex {
	staticOnce.Do(func() {
		sub, err := fs.Sub(gobill.FS, "web/static")
		if err != nil {
			panic(err) // the path is embedded at build time
		}
		all := sha256.New()
		staticIdx.etags = map[string]string{}
		err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(sub, p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			etag := `"` + hex.EncodeToString(sum[:12]) + `"`
			staticIdx.etags[p] = etag
			all.Write([]byte(p + "\x00" + etag + "\n"))
			return nil
		})
		if err != nil {
			panic(err)
		}
		staticIdx.hash = hex.EncodeToString(all.Sum(nil)[:6])
	})
	return staticIdx
}

// asset returns p with the version token: the release version, or the content hash for dev builds.
// Dev builds share one version string ("dev" or "dev-<commit>"), so only the hash changes after a CSS rebuild.
func (s *Server) asset(p string) string {
	tok := s.Version
	if tok == "" || strings.HasPrefix(tok, "dev") {
		tok = staticFiles().hash
	}
	return p + "?v=" + url.QueryEscape(tok)
}

// staticHandler serves web/static. A URL with a version token is immutable for a year. Without one the
// browser revalidates, and the content-hash ETag turns that into a 304 (embedded files have no modtime).
func staticHandler(fsys fs.FS) http.Handler {
	files := http.StripPrefix("/static/", http.FileServerFS(fsys))
	etags := staticFiles().etags
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("v") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if e, ok := etags[strings.TrimPrefix(r.URL.Path, "/static/")]; ok {
			w.Header().Set("ETag", e)
		}
		files.ServeHTTP(w, r)
	})
}

// cacheDefaults sets Cache-Control on every response before the handler runs, so a handler that sets
// its own value (static files, no-store JSON) overrides it. Pages are private and revalidated, so
// Cloudflare and browsers never keep an old page. Uploads may be cached for an hour.
func cacheDefaults(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w.Header().Get("Cache-Control") == "" {
			w.Header().Set("Cache-Control", "private, no-cache")
			if strings.HasPrefix(r.URL.Path, "/uploads/") {
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
		}
		next.ServeHTTP(w, r)
	})
}
