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

// guideSlugs is the whitelist of guides, in index order. Must match the files in nuxbill.Docs.
var guideSlugs = []string{"README", "instalasi", "konfigurasi", "mikrotik", "freeradius-rest", "migrasi-phpnuxbill", "keamanan", "monitoring"}

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
}

// docsView is the template data of both /admin/docs pages. Guide is nil on the index.
type docsView struct {
	Guide *guide
	Index []*guide
}

// loadGuides renders every whitelisted guide. A missing file is a startup error.
func loadGuides(fsys fs.FS) (*guideSet, error) {
	gs := &guideSet{bySlug: map[string]*guide{}}
	for _, slug := range guideSlugs {
		src, err := fs.ReadFile(fsys, "docs/"+slug+".md")
		if err != nil {
			return nil, fmt.Errorf("guide %s: %w", slug, err)
		}
		g, err := renderGuide(slug, src)
		if err != nil {
			return nil, fmt.Errorf("guide %s: %w", slug, err)
		}
		gs.list = append(gs.list, g)
		gs.bySlug[slug] = g
	}
	return gs, nil
}

func renderGuide(slug string, src []byte) (*guide, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		// no html.WithUnsafe: raw HTML in the Markdown is dropped and javascript: links are blanked
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(docLinks{from: "docs/" + slug + ".md"}, 100)),
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
	if path.Dir(p) == "docs" && strings.HasSuffix(p, ".md") {
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
	s.render(w, r, http.StatusOK, "docs", Page{Title: "Guide", Data: docsView{Index: s.guides.list}})
}

func (s *Server) docsPage(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guides.bySlug[r.PathValue("slug")] // whitelist: anything else is a 404
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "docs", Page{Title: g.Title, Data: docsView{Guide: g, Index: s.guides.list}})
}
