package web

// In-app guide ("Panduan"): the operator docs from docs/ rendered once at startup.

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// guideSlugs is the whitelist of guides, in index order. Each slug exists in docs/id and docs/en.
var guideSlugs = []string{"README", "installation", "upgrade", "migration-phpnuxbill", "configuration", "integrations", "security", "mikrotik", "freeradius-rest", "monitoring", "backup-restore"}

// docsLangs are the guide languages: the folder under docs/ and the value of ?lang=.
var docsLangs = []string{"id", "en"}

// guideGroups is the topic order of the document picker, per language. Every slug appears once.
var guideGroups = map[string][]guideGroup{
	"id": {
		{"Indeks", []string{"README"}},
		{"Mulai", []string{"installation", "upgrade", "migration-phpnuxbill"}},
		{"Konfigurasi", []string{"configuration", "integrations", "security"}},
		{"Jaringan", []string{"mikrotik", "freeradius-rest"}},
		{"Operasional", []string{"monitoring", "backup-restore"}},
	},
	"en": {
		{"Index", []string{"README"}},
		{"Getting started", []string{"installation", "upgrade", "migration-phpnuxbill"}},
		{"Configuration", []string{"configuration", "integrations", "security"}},
		{"Network", []string{"mikrotik", "freeradius-rest"}},
		{"Operations", []string{"monitoring", "backup-restore"}},
	},
}

type guideGroup struct {
	Name  string
	Slugs []string
}

// docsLangCookie remembers the language a viewer picked with the ID | EN toggle.
const docsLangCookie = "gobill_docs_lang"

const repoURL = "https://github.com/frand-kod/gobill/blob/main/"

type tocItem struct {
	Level int
	ID    string
	Text  string
}

type guide struct {
	Slug  string
	Title string // first H1
	Desc  string // first paragraph, plain text
	HTML  template.HTML
	TOC   []tocItem // H2 and H3
}

type guideSet struct {
	list   []*guide // index order
	bySlug map[string]*guide
	groups []pickerGroup // document picker, grouped by topic
}

type pickerGroup struct {
	Name  string
	Items []*guide
}

// langLink is one half of the ID | EN toggle.
type langLink struct {
	Lang, Label, Href string
	Active            bool
}

// docsView is the template data of both /admin/docs pages. Guide is nil on the index.
type docsView struct {
	Guide  *guide
	Slug   string // slug of Guide, empty on the index
	Index  []*guide
	Lang   string
	Groups []pickerGroup
	Toggle []langLink
}

// loadAllGuides renders the guides of every language. A missing file is a startup error.
func loadAllGuides(fsys fs.FS) (map[string]*guideSet, error) {
	out := map[string]*guideSet{}
	for _, lang := range docsLangs {
		gs, err := loadGuides(fsys, lang)
		if err != nil {
			return nil, err
		}
		out[lang] = gs
	}
	return out, nil
}

// loadGuides renders every whitelisted guide of one language from docs/<lang>/. A missing file
// is a startup error, and so is a slug that the picker does not list.
func loadGuides(fsys fs.FS, lang string) (*guideSet, error) {
	gs := &guideSet{bySlug: map[string]*guide{}}
	for _, slug := range guideSlugs {
		src, err := fs.ReadFile(fsys, "docs/"+lang+"/"+slug+".md")
		if err != nil {
			return nil, fmt.Errorf("guide %s/%s: %w", lang, slug, err)
		}
		g, err := renderGuide(lang, slug, src)
		if err != nil {
			return nil, fmt.Errorf("guide %s/%s: %w", lang, slug, err)
		}
		gs.list = append(gs.list, g)
		gs.bySlug[slug] = g
	}
	listed := map[string]bool{}
	for _, grp := range guideGroups[lang] {
		pg := pickerGroup{Name: grp.Name}
		for _, slug := range grp.Slugs {
			g, ok := gs.bySlug[slug]
			if !ok {
				return nil, fmt.Errorf("picker %s: unknown slug %s", lang, slug)
			}
			listed[slug] = true
			pg.Items = append(pg.Items, g)
		}
		gs.groups = append(gs.groups, pg)
	}
	for _, slug := range guideSlugs {
		if !listed[slug] {
			return nil, fmt.Errorf("picker %s: slug %s is not in any group", lang, slug)
		}
	}
	return gs, nil
}

// docsLang picks the guide language for this request. ?lang= wins and is remembered in a cookie.
// Without either, the app language decides: Indonesian gives id, anything else gives en.
func (s *Server) docsLang(w http.ResponseWriter, r *http.Request) string {
	lang := r.URL.Query().Get("lang")
	if validDocsLang(lang) {
		http.SetCookie(w, &http.Cookie{Name: docsLangCookie, Value: lang, Path: "/admin/docs",
			MaxAge: 365 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.sessions.Cookie.Secure})
		return lang
	}
	if c, err := r.Cookie(docsLangCookie); err == nil && validDocsLang(c.Value) {
		return c.Value
	}
	if s.language() == "indonesia" {
		return "id"
	}
	return "en"
}

func validDocsLang(lang string) bool { return lang == "id" || lang == "en" }

// docsToggle builds the ID | EN links for the page at base, keeping the same page.
func docsToggle(base, cur string) []langLink {
	var out []langLink
	for _, lang := range docsLangs {
		out = append(out, langLink{Lang: lang, Label: strings.ToUpper(lang), Href: base + "?lang=" + lang, Active: lang == cur})
	}
	return out
}

func renderGuide(lang, slug string, src []byte) (*guide, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		// no html.WithUnsafe: raw HTML in the Markdown is dropped and javascript: links are blanked
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(docLinks{from: "docs/" + lang + "/" + slug + ".md"}, 100)),
		),
	)
	doc := md.Parser().Parse(gmtext.NewReader(src))
	g := &guide{Slug: slug}
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			t := string(n.Text(src))
			if n.Level == 1 && g.Title == "" {
				g.Title = t
			}
			if n.Level == 2 || n.Level == 3 {
				id, _ := n.AttributeString("id")
				ids, _ := id.([]byte)
				g.TOC = append(g.TOC, tocItem{Level: n.Level, ID: string(ids), Text: t})
			}
		case *ast.Paragraph:
			if g.Desc == "" {
				g.Desc = string(n.Text(src))
			}
		}
		return ast.WalkContinue, nil
	})
	if g.Title == "" {
		return nil, fmt.Errorf("no H1 title")
	}
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return nil, err
	}
	g.HTML = template.HTML(buf.String())
	return g, nil
}

// docLinks rewrites relative links: between whitelisted guides to /admin/docs/<slug>, anything
// else (excluded docs, root files) to the GitHub page of that file.
type docLinks struct{ from string }

func (t docLinks) Transform(doc *ast.Document, _ gmtext.Reader, _ parser.Context) {
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if l, ok := n.(*ast.Link); ok && entering {
			l.Destination = []byte(rewriteLink(t.from, string(l.Destination)))
		}
		return ast.WalkContinue, nil
	})
}

func rewriteLink(from, dest string) string {
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
		return dest // in-page anchors, absolute URLs, root-relative paths
	}
	frag := ""
	if u.Fragment != "" {
		frag = "#" + u.Fragment
	}
	p := path.Join(path.Dir(from), u.Path)
	if strings.HasPrefix(p, "..") {
		return dest
	}
	// same language folder only: links keep the viewer on the language they are reading
	if path.Dir(p) == path.Dir(from) && strings.HasSuffix(p, ".md") {
		slug := strings.TrimSuffix(path.Base(p), ".md")
		for _, s := range guideSlugs {
			if s == slug {
				return "/admin/docs/" + slug + frag
			}
		}
	}
	return repoURL + p + frag
}

func (s *Server) docsIndex(w http.ResponseWriter, r *http.Request) {
	lang := s.docsLang(w, r)
	gs := s.guides[lang]
	v := docsView{Index: gs.list, Lang: lang, Groups: gs.groups, Toggle: docsToggle("/admin/docs", lang)}
	s.render(w, r, http.StatusOK, "docs", Page{Title: "Guide", Data: v})
}

func (s *Server) docsPage(w http.ResponseWriter, r *http.Request) {
	lang := s.docsLang(w, r)
	gs := s.guides[lang]
	g, ok := gs.bySlug[r.PathValue("slug")] // whitelist: anything else is a 404
	if !ok {
		http.NotFound(w, r)
		return
	}
	v := docsView{Guide: g, Slug: g.Slug, Index: gs.list, Lang: lang, Groups: gs.groups, Toggle: docsToggle("/admin/docs/"+g.Slug, lang)}
	s.render(w, r, http.StatusOK, "docs", Page{Title: g.Title, Data: v})
}
