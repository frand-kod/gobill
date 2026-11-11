package core

import (
	"bytes"
	"log/slog"
	"net/http"
)

// Render writes a page. The body is buffered so a template error gives a 500, not half a page.
// Decorate adds the parts only some pages have.
func (s *Core) Render(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	p.Admin = AdminFrom(r)
	p.Path = r.URL.Path
	ctx := r.Context()
	for _, a := range []struct {
		dst *string
		key string
	}{{&p.Flash, "flash"}, {&p.Error, "error"}, {&p.Warn, "warn"}, {&p.Detail, "detail"}, {&p.ErrLink, "errlink"}} {
		if v := s.Sessions.PopString(ctx, a.key); *a.dst == "" {
			*a.dst = v
		}
	}
	if s.Sessions.PopString(ctx, "flash_sticky") != "" {
		p.Sticky = true
	}
	p.Lang = s.Language()
	p.Version = s.Version
	if s.Decorate != nil {
		s.Decorate(r, name, &p)
	}
	p.Dir = "ltr"
	if p.Lang == "arabic" {
		p.Dir = "rtl"
	}
	var buf bytes.Buffer
	if err := s.Templates[name].ExecuteTemplate(&buf, "base", p); err != nil {
		slog.Error("render", "page", name, "err", err)
		s.ErrorPage(w, "-")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}
