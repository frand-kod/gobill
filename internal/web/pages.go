package web

// Editable static content pages (admin editor and public view).

import (
	"database/sql"
	"net/http"

	"errors"
	"github.com/frand-kod/gobill/internal/db"
	"strings"
)

// Old pages.php: four static pages (announcement, terms, privacy, registration) edited as plain text.

// pageTabs is the menu shared by the page editor and the custom-field list.
func pageTabs() []option {
	var tabs []option
	for _, p := range []struct{ slug, title string }{{"announcement", "Announcement"}, {"tos", "Terms and Conditions"},
		{"privacy", "Privacy Policy"}, {"registration", "Registration Info"}} {
		tabs = append(tabs, option{"/admin/pages/" + p.slug, p.title})
	}
	return append(tabs, option{"/admin/fields", "Custom Fields"})
}

func (s *Server) pageEdit(w http.ResponseWriter, r *http.Request) {
	p, err := s.queries.GetPage(r.Context(), r.PathValue("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get page", err)
		return
	}
	fp := formPage{Heading: p.Title, Action: "/admin/pages/" + p.Slug, Cancel: "/admin", Fields: []field{{
		Name: "body", Label: "Content", Type: "textarea", Value: p.Body,
		Hint: "Plain text. Line breaks are kept; HTML is shown as text.",
	}}}
	s.render(w, r, http.StatusOK, "form", Page{Title: p.Title, Tabs: pageTabs(), Data: fp})
}

func (s *Server) pageSave(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	p, err := s.queries.GetPage(r.Context(), slug)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get page", err)
		return
	}
	body := strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n")
	if err := s.queries.UpdatePageBody(r.Context(), db.UpdatePageBodyParams{Body: body, Slug: slug}); err != nil {
		s.fail(w, "save page", err)
		return
	}
	s.done(w, r, "/admin/pages/"+slug, "Data Updated Successfully", "page.update", p.Title)
}

// pagePublic shows a static page to anyone. The body is escaped; whitespace-pre-line keeps the line breaks.
func (s *Server) pagePublic(w http.ResponseWriter, r *http.Request) {
	p, err := s.queries.GetPage(r.Context(), r.PathValue("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get page", err)
		return
	}
	s.prender(w, r, http.StatusOK, "p_page", Page{Title: p.Title, Data: p})
}
