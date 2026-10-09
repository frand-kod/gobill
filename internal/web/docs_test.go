package web

import (
	"io/fs"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	nuxbill "github.com/frand-kod/gobill"
)

func TestDocsIndexListsOnlyWhitelist(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	w := do(h, "GET", "/admin/docs", nil, c)
	if w.Code != http.StatusOK {
		t.Fatalf("index: %d", w.Code)
	}
	body := w.Body.String()
	if n := strings.Count(body, `href="/admin/docs/`); n != len(guideSlugs) {
		t.Fatalf("index has %d guide links, want %d", n, len(guideSlugs))
	}
	for _, slug := range guideSlugs {
		if !strings.Contains(body, `href="/admin/docs/`+slug+`"`) {
			t.Errorf("index misses %s", slug)
		}
	}
	for _, excluded := range []string{"PROGRESS", "UI-PARITY", "arsitektur", "pengembangan", "plan"} {
		if strings.Contains(body, "/admin/docs/"+excluded) {
			t.Errorf("index links excluded doc %s", excluded)
		}
	}
}

func TestDocsSlugWhitelist(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	for _, slug := range []string{"PROGRESS", "UI-PARITY", "konfigurasi.md", "..%2Fkonfigurasi", "%2E%2E", "nope"} {
		if w := do(h, "GET", "/admin/docs/"+slug, nil, c); w.Code != http.StatusNotFound {
			t.Errorf("slug %q: got %d, want 404", slug, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/docs/konfigurasi", nil, c); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Konfigurasi") {
		t.Fatalf("konfigurasi: %d", w.Code)
	}
}

func TestDocsRawHTMLEscaped(t *testing.T) {
	g, err := renderGuide("x", []byte("# Judul\n\n<script>alert(1)</script>\n\n<b>tebal</b> teks\n\n[klik](javascript:alert(1))\n"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(g.HTML)
	for _, bad := range []string{"<script", "<b>", "javascript:"} {
		if strings.Contains(out, bad) {
			t.Errorf("rendered HTML contains %q: %s", bad, out)
		}
	}
}

func TestDocsRewriteLinks(t *testing.T) {
	for _, tc := range []struct{ from, dest, want string }{
		{"docs/instalasi.md", "konfigurasi.md#x", "/admin/docs/konfigurasi#x"},
		{"docs/README.md", "keamanan.md", "/admin/docs/keamanan"},
		{"docs/README.md", "../README.md", repoURL + "README.md"},
		{"docs/README.md", "../CHANGELOG.md", repoURL + "CHANGELOG.md"},
		{"docs/README.md", "PROGRESS.md", repoURL + "docs/PROGRESS.md"},
		{"docs/README.md", "plan/README.md", repoURL + "docs/plan/README.md"},
		{"docs/instalasi.md", "#atas", "#atas"},
		{"docs/instalasi.md", "https://example.com/a", "https://example.com/a"},
		{"docs/instalasi.md", "/admin", "/admin"},
	} {
		if got := rewriteLink(tc.from, tc.dest); got != tc.want {
			t.Errorf("rewriteLink(%s, %s) = %s, want %s", tc.from, tc.dest, got, tc.want)
		}
	}
}

func TestDocsCrossLinkAndAnchorRendered(t *testing.T) {
	gs, err := loadGuides(nuxbill.Docs)
	if err != nil {
		t.Fatal(err)
	}
	if out := string(gs.bySlug["migrasi-phpnuxbill"].HTML); !strings.Contains(out, `href="/admin/docs/mikrotik#pelajaran-dari-uji-lapangan"`) {
		t.Fatal("cross-guide link not rewritten")
	}
	if out := string(gs.bySlug["mikrotik"].HTML); !strings.Contains(out, `id="pelajaran-dari-uji-lapangan"`) {
		t.Fatal("heading id missing, anchor would be dead")
	}
	// every in-guide anchor must point at a heading that exists in the target guide
	link := regexp.MustCompile(`href="/admin/docs/([\w-]+)#([\w-]+)"`)
	for _, g := range gs.list {
		for _, m := range link.FindAllStringSubmatch(string(g.HTML), -1) {
			target := gs.bySlug[m[1]]
			if target == nil || !strings.Contains(string(target.HTML), `id="`+m[2]+`"`) {
				t.Errorf("%s: dead anchor %s#%s", g.Slug, m[1], m[2])
			}
		}
	}
}

func TestDocsStaffReadAnonymousRedirected(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(h, "GET", "/admin/docs/keamanan", nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous: got %d %q", w.Code, w.Header().Get("Location"))
	}
	// the Report role is the most limited one and may read the guides too
	c := login(t, h, "rita")
	if w := do(h, "GET", "/admin/docs", nil, c); w.Code != http.StatusOK {
		t.Fatalf("report role index: %d", w.Code)
	}
}

// Guard: only the whitelisted Markdown files are embedded, never SQL dumps or internal notes.
func TestDocsEmbedIsWhitelistOnly(t *testing.T) {
	var got []string
	err := fs.WalkDir(nuxbill.Docs, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(p, ".sql") {
			t.Errorf("embedded SQL file %s", p)
		}
		got = append(got, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, s := range guideSlugs {
		want = append(want, "docs/"+s+".md")
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("embedded docs = %v, want %v", got, want)
	}
}
