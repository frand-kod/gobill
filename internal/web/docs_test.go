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

// internal docs must never show up in the app, in either language
var internalSlugs = []string{"progres", "paritas-ui", "paritas-bisnis", "arsitektur", "pengembangan", "rencana", "audit-ux", "audit-alur-ux", "panduan-menulis-docs"}

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
	for _, excluded := range internalSlugs {
		if strings.Contains(body, "/admin/docs/"+excluded) {
			t.Errorf("index links excluded doc %s", excluded)
		}
	}
}

func TestDocsSlugWhitelist(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	for _, slug := range append([]string{"configuration.md", "..%2Fconfiguration", "%2E%2E", "nope", "konfigurasi"}, internalSlugs...) {
		if w := do(h, "GET", "/admin/docs/"+slug, nil, c); w.Code != http.StatusNotFound {
			t.Errorf("slug %q: got %d, want 404", slug, w.Code)
		}
	}
	if w := do(h, "GET", "/admin/docs/configuration", nil, c); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Konfigurasi") {
		t.Fatalf("configuration (id): %d", w.Code)
	}
}

func TestDocsRawHTMLEscaped(t *testing.T) {
	g, err := renderGuide("id", "x", []byte("# Judul\n\n<script>alert(1)</script>\n\n<b>tebal</b> teks\n\n[klik](javascript:alert(1))\n"))
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
		{"docs/id/installation.md", "configuration.md#x", "/admin/docs/configuration#x"},
		{"docs/en/installation.md", "configuration.md#x", "/admin/docs/configuration#x"},
		{"docs/id/README.md", "security.md", "/admin/docs/security"},
		{"docs/id/README.md", "../../README.md", repoURL + "README.md"},
		{"docs/id/README.md", "../../CHANGELOG.md", repoURL + "CHANGELOG.md"},
		{"docs/id/README.md", "../internal/progres.md", repoURL + "docs/internal/progres.md"},
		{"docs/id/README.md", "../internal/rencana/README.md", repoURL + "docs/internal/rencana/README.md"},
		{"docs/id/installation.md", "#atas", "#atas"},
		{"docs/id/installation.md", "https://example.com/a", "https://example.com/a"},
		{"docs/id/installation.md", "/admin", "/admin"},
	} {
		if got := rewriteLink(tc.from, tc.dest); got != tc.want {
			t.Errorf("rewriteLink(%s, %s) = %s, want %s", tc.from, tc.dest, got, tc.want)
		}
	}
}

func TestDocsCrossLinkAndAnchorRendered(t *testing.T) {
	gs, err := loadAllGuides(nuxbill.Docs)
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]string{"id": "pelajaran-dari-uji-lapangan", "en": "lessons-from-field-testing"}
	for lang, anchor := range anchors {
		set := gs[lang]
		if out := string(set.bySlug["migration-phpnuxbill"].HTML); !strings.Contains(out, `href="/admin/docs/mikrotik#`+anchor+`"`) {
			t.Fatalf("%s: cross-guide link not rewritten", lang)
		}
		if out := string(set.bySlug["mikrotik"].HTML); !strings.Contains(out, `id="`+anchor+`"`) {
			t.Fatalf("%s: heading id missing, anchor would be dead", lang)
		}
		// every in-guide anchor must point at a heading that exists in the target guide
		link := regexp.MustCompile(`href="/admin/docs/([\w-]+)#([\w-]+)"`)
		for _, g := range set.list {
			for _, m := range link.FindAllStringSubmatch(string(g.HTML), -1) {
				target := set.bySlug[m[1]]
				if target == nil || !strings.Contains(string(target.HTML), `id="`+m[2]+`"`) {
					t.Errorf("%s/%s: dead anchor %s#%s", lang, g.Slug, m[1], m[2])
				}
			}
		}
	}
}

func TestDocsEveryGuideInBothLanguages(t *testing.T) {
	gs, err := loadAllGuides(nuxbill.Docs)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range docsLangs {
		for _, slug := range guideSlugs {
			g := gs[lang].bySlug[slug]
			if g == nil || g.Title == "" || len(g.HTML) == 0 {
				t.Errorf("%s/%s: missing or empty", lang, slug)
			}
		}
	}
	// the two languages must be parallel: same slugs, so a toggle keeps the same page
	if len(gs["id"].list) != len(gs["en"].list) {
		t.Fatalf("id has %d guides, en has %d", len(gs["id"].list), len(gs["en"].list))
	}
}

func TestDocsLanguageToggleSwitchesFolder(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	// the test server uses the Indonesian app language, so the default is id
	if w := do(h, "GET", "/admin/docs/configuration", nil, c); !strings.Contains(w.Body.String(), `page-title">Konfigurasi</h1>`) {
		t.Fatal("default for the Indonesian app should be id")
	}
	w := do(h, "GET", "/admin/docs/configuration?lang=en", nil, c)
	if !strings.Contains(w.Body.String(), `page-title">Configuration</h1>`) {
		t.Fatal("?lang=en should show the English guide")
	}
	var remembered *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == docsLangCookie {
			remembered = ck
		}
	}
	if remembered == nil || remembered.Value != "en" {
		t.Fatal("?lang=en should be remembered in a cookie")
	}
	// the cookie alone keeps the choice on the next page
	w = do(h, "GET", "/admin/docs/security", nil, nil, "Cookie", c.Name+"="+c.Value+"; "+docsLangCookie+"=en")
	if !strings.Contains(w.Body.String(), `page-title">Security</h1>`) {
		t.Fatal("remembered language should apply without ?lang=")
	}
	// an unknown value is ignored
	if w := do(h, "GET", "/admin/docs/security?lang=xx", nil, c); !strings.Contains(w.Body.String(), `page-title">Keamanan</h1>`) {
		t.Fatal("?lang=xx should fall back to the app language")
	}
}

func TestDocsPickerListsEverySlug(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	for _, lang := range docsLangs {
		w := do(h, "GET", "/admin/docs/mikrotik?lang="+lang, nil, c)
		body := w.Body.String()
		if !strings.Contains(body, "<select") || !strings.Contains(body, "<optgroup") {
			t.Fatalf("%s: picker missing", lang)
		}
		for _, slug := range guideSlugs {
			if !strings.Contains(body, `value="/admin/docs/`+slug+`?lang=`+lang+`"`) {
				t.Errorf("%s: picker misses %s", lang, slug)
			}
		}
		if !strings.Contains(body, `value="/admin/docs/mikrotik?lang=`+lang+`" selected`) {
			t.Errorf("%s: current doc not selected", lang)
		}
	}
}

func TestDocsStaffReadAnonymousRedirected(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(h, "GET", "/admin/docs/security", nil, nil)
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
	for _, lang := range docsLangs {
		for _, s := range guideSlugs {
			want = append(want, "docs/"+lang+"/"+s+".md")
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("embedded docs = %v, want %v", got, want)
	}
}
