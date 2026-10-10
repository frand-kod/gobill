package web

// Template parsing, icon loading, page rendering and flash/redirect helpers.

import (
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"

	"bytes"
	"fmt"
	nuxbill "github.com/frand-kod/gobill"
	"path"
	"strings"
)

func (s *Server) flashTo(w http.ResponseWriter, r *http.Request, to, msg string) {
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), msg))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// safeRedirect returns u only when it is an absolute http(s) URL.
func safeRedirect(u string) string {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		return ""
	}
	return p.String()
}

// ---- templates ----

// navActive returns the entry of keys that owns path: the longest key equal to path or one of its parents.
// "/admin" only matches itself, otherwise the dashboard would be active on every admin page.
// Returns "" when no key matches.
func navActive(path string, keys ...string) string {
	best := ""
	for _, k := range keys {
		if len(k) <= len(best) {
			continue
		}
		if path == k || (k != "/admin" && strings.HasPrefix(path, k+"/")) {
			best = k
		}
	}
	return best
}

func (s *Server) parseTemplates() error {
	icons, err := loadIcons()
	if err != nil {
		return err
	}
	funcs := template.FuncMap{
		"T": func(text string) string { return s.catalog.T(s.language(), text) },
		// TT translates the part before ": ", so headings like "Edit Contact: budi" work.
		"TT": func(text string) string {
			if head, rest, ok := strings.Cut(text, ": "); ok {
				return s.catalog.T(s.language(), head) + ": " + rest
			}
			return s.catalog.T(s.language(), text)
		},
		"hasPrefix": strings.HasPrefix,
		"navActive": navActive,
		"money":     money,
		"badge":     badge,
		"ts":        s.ts,
		"icon": func(name string) (template.HTML, error) {
			svg, ok := icons[name]
			if !ok {
				return "", fmt.Errorf("unknown icon %q", name)
			}
			return svg, nil
		},
	}
	pages := map[string][]string{
		"login":            {"base.html", "login.html"},
		"dashboard":        {"base.html", "app.html", "dashboard.html"},
		"list":             {"base.html", "app.html", "list.html"},
		"form":             {"base.html", "app.html", "form.html", "customer_pick.html"},
		"customer":         {"base.html", "app.html", "customer.html", "radius_usage.html"},
		"radius_sessions":  {"base.html", "app.html", "radius_sessions.html"},
		"print":            {"print.html"},
		"voucher_view":     {"base.html", "app.html", "voucher_view.html"},
		"recharge_confirm": {"base.html", "app.html", "recharge_confirm.html"},
		"recharge":         {"base.html", "app.html", "recharge.html", "customer_pick.html"},
		"report":           {"base.html", "app.html", "report.html"},
		"report_print":     {"report_print.html"},
		"invoice":          {"invoice.html"},
		"maps":             {"base.html", "app.html", "maps.html"},
		"pay_audit":        {"base.html", "app.html", "pay_audit.html"},
		"docs":             {"base.html", "app.html", "docs.html"},
		"network":          {"base.html", "app.html", "network.html"},

		"p_login":       {"base.html", "portal/login.html"},
		"p_register":    {"base.html", "portal/register.html"},
		"p_dashboard":   {"base.html", "portal/layout.html", "portal/dashboard.html"},
		"p_profile":     {"base.html", "portal/layout.html", "portal/profile.html"},
		"p_orders":      {"base.html", "portal/layout.html", "portal/orders.html"},
		"p_plans":       {"base.html", "portal/layout.html", "portal/plans.html"},
		"p_inbox":       {"base.html", "portal/layout.html", "portal/inbox.html"},
		"p_voucher":     {"base.html", "portal/layout.html", "portal/voucher.html"},
		"p_forgot":      {"base.html", "portal/forgot.html"},
		"p_page":        {"base.html", "portal/page.html"},
		"p_payment":     {"base.html", "portal/layout.html", "portal/payment.html"},
		"p_activation":  {"base.html", "portal/layout.html", "portal/activation.html"},
		"p_friend":      {"base.html", "portal/layout.html", "portal/friend.html"},
		"p_forgot_user": {"base.html", "portal/forgot_user.html"},
	}
	s.templates = map[string]*template.Template{}
	for name, files := range pages {
		for i, f := range files {
			files[i] = path.Join("web/templates", f)
		}
		t, err := template.New("").Funcs(funcs).ParseFS(nuxbill.FS, files...)
		if err != nil {
			return err
		}
		s.templates[name] = t
	}
	return nil
}

// loadIcons reads web/static/icons/*.svg, drops the license comment and
// marks each icon decorative.
func loadIcons() (map[string]template.HTML, error) {
	files, err := fs.Glob(nuxbill.FS, "web/static/icons/*.svg")
	if err != nil {
		return nil, err
	}
	icons := map[string]template.HTML{}
	for _, f := range files {
		b, err := fs.ReadFile(nuxbill.FS, f)
		if err != nil {
			return nil, err
		}
		i := bytes.Index(b, []byte("<svg"))
		if i < 0 {
			return nil, fmt.Errorf("%s: no <svg element", f)
		}
		svg := bytes.Replace(b[i:], []byte("<svg"), []byte(`<svg aria-hidden="true"`), 1)
		icons[strings.TrimSuffix(path.Base(f), ".svg")] = template.HTML(svg)
	}
	return icons, nil
}

// render writes a page. The body is buffered so a template error gives a 500, not half a page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	p.Admin = adminFrom(r)
	p.Path = r.URL.Path
	ctx := r.Context()
	for _, a := range []struct {
		dst *string
		key string
	}{{&p.Flash, "flash"}, {&p.Error, "error"}, {&p.Warn, "warn"}, {&p.Detail, "detail"}, {&p.ErrLink, "errlink"}} {
		if v := s.sessions.PopString(ctx, a.key); *a.dst == "" {
			*a.dst = v
		}
	}
	if p.Intro.Title == "" {
		p.Intro = introFor(name, r.URL.Path)
	}
	p.Intro = s.translateIntro(p.Intro)
	p.Lang = s.language()
	p.Version = s.Version
	p.Brand = s.brand(r.Context())
	p.Dir = "ltr"
	if p.Lang == "arabic" {
		p.Dir = "rtl"
	}
	var buf bytes.Buffer
	if err := s.templates[name].ExecuteTemplate(&buf, "base", p); err != nil {
		slog.Error("render", "page", name, "err", err)
		s.errorPage(w, "-")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}
