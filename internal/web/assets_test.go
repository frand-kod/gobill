package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestStaticAssetCacheHeaders(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")

	page := do(h, "GET", "/admin", nil, c)
	wantCode(t, page, 200, "admin page")
	if !strings.Contains(page.Body.String(), `href="/static/app.css?v=`) {
		t.Fatal("admin page does not reference /static/app.css with a version token")
	}
	if cc := page.Header().Get("Cache-Control"); cc != "private, no-cache" {
		t.Errorf("HTML Cache-Control = %q, want private, no-cache", cc)
	}

	versioned := do(h, "GET", "/static/app.css?v=x", nil, nil)
	wantCode(t, versioned, 200, "versioned css")
	if cc := versioned.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("versioned static Cache-Control = %q", cc)
	}

	plain := do(h, "GET", "/static/app.css", nil, nil)
	wantCode(t, plain, 200, "unversioned css")
	if cc := plain.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("unversioned static Cache-Control = %q, want no-cache", cc)
	}
	etag := plain.Header().Get("ETag")
	if etag == "" {
		t.Fatal("unversioned static has no ETag")
	}
	revalidate := do(h, "GET", "/static/app.css", nil, nil, "If-None-Match", etag)
	wantCode(t, revalidate, http.StatusNotModified, "revalidation with If-None-Match")
}
