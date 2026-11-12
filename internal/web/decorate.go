package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/frand-kod/gobill/internal/update"
	"github.com/frand-kod/gobill/internal/web/core"
)

// decorate fills the parts of a Page that depend on the admin and the app state. core.Render calls it
// after the flash messages are read and before the template runs.
func (s *Server) decorate(r *http.Request, name string, p *Page) {
	ctx := r.Context()
	if p.Intro.Title == "" {
		p.Intro = core.Intro(introFor(name, r.URL.Path))
	}
	p.Intro = core.Intro(s.translateIntro(intro(p.Intro)))
	p.Brand = s.Brand(ctx)
	if p.Admin != nil && p.Admin.Role == "SuperAdmin" && s.UpdateInfo != nil {
		if tag, link := s.UpdateInfo(); update.Newer(tag, s.Version) {
			p.UpdateTag, p.UpdateURL = tag, link
		}
	}
	if p.Admin != nil && p.Admin.Role == "SuperAdmin" {
		if n, err := s.setupNavFor(ctx, r.URL.Path); err != nil {
			slog.Warn("setup nav", "err", err)
		} else {
			p.Setup = n
		}
	}
	if p.Admin != nil {
		if items, err := s.bellItems(ctx, time.Now()); err != nil {
			slog.Warn("admin bell", "err", err)
		} else {
			p.Notices = attentionFor(items, p.Admin)
			for _, it := range p.Notices {
				p.NoticeTotal += it.Count
				p.NoticeErr = p.NoticeErr || it.Err
			}
		}
	}
}
