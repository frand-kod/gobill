package public

// Editable static content pages, public view. The admin editor is in web.

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
)

// pagePublic shows a static page to anyone. The body is escaped; whitespace-pre-line keeps the line breaks.
func (s *Handlers) pagePublic(w http.ResponseWriter, r *http.Request) {
	p, err := s.Queries.GetPage(r.Context(), r.PathValue("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get page", err)
		return
	}
	s.Prender(w, r, http.StatusOK, "p_page", core.Page{Title: p.Title, Data: p})
}
