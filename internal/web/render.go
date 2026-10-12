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
	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/update"
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
		"asset":     s.asset,
		"spark":     sparkline,
		"navActive": navActive,
		"money":     money,
		"badge":     badge,
		"dot":       func(v string) string { return badgeDot(badge(v)) },
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
		"login_2fa":        {"base.html", "login_2fa.html"},
		"admin_2fa":        {"base.html", "admin/app.html", "admin/admin_2fa.html"},
		"dashboard":        {"base.html", "admin/app.html", "admin/dashboard.html"},
		"status":           {"base.html", "admin/app.html", "admin/status.html"},
		"list":             {"base.html", "admin/app.html", "admin/list.html"},
		"form":             {"base.html", "admin/app.html", "admin/form.html", "admin/formfields.html", "admin/customer_pick.html", "admin/tplvars.html"},
		"customer":         {"base.html", "admin/app.html", "admin/customer.html", "admin/radius_usage.html", "admin/recharge_dialog.html"},
		"radius_sessions":  {"base.html", "admin/app.html", "admin/radius_sessions.html"},
		"print":            {"admin/print.html"},
		"voucher_view":     {"base.html", "admin/app.html", "admin/voucher_view.html"},
		"recharge_confirm": {"base.html", "admin/app.html", "admin/recharge_confirm.html", "admin/recharge_dialog.html"},
		"recharge":         {"base.html", "admin/app.html", "admin/recharge.html", "admin/customer_pick.html", "admin/customer_summary.html", "admin/recharge_dialog.html"},
		"report":           {"base.html", "admin/app.html", "admin/report.html"},
		"report_print":     {"admin/report_print.html"},
		"invoice":          {"admin/invoice.html"},
		"maps":             {"base.html", "admin/app.html", "admin/maps.html"},
		"pay_audit":        {"base.html", "admin/app.html", "admin/pay_audit.html"},
		"docs":             {"base.html", "admin/app.html", "admin/docs.html"},
		"network":          {"base.html", "admin/app.html", "admin/network.html"},
		"import":           {"base.html", "admin/app.html", "admin/import.html"},
		"restore":          {"base.html", "admin/app.html", "admin/restore.html"},
		"search":           {"base.html", "admin/app.html", "admin/search.html"},

		"p_login":        {"base.html", "portal/login.html"},
		"p_qris":         {"base.html", "portal/qris.html"},
		"p_register":     {"base.html", "portal/register.html"},
		"p_dashboard":    {"base.html", "portal/layout.html", "portal/dashboard.html", "portal/dash_cards.html", "promo_cards.html"},
		"p_connection":   {"base.html", "portal/layout.html", "portal/connection.html"},
		"p_profile":      {"base.html", "portal/layout.html", "portal/profile.html"},
		"p_orders":       {"base.html", "portal/layout.html", "portal/orders.html"},
		"p_plans":        {"base.html", "portal/layout.html", "portal/plans.html"},
		"p_inbox":        {"base.html", "portal/layout.html", "portal/inbox.html"},
		"p_voucher":      {"base.html", "portal/layout.html", "portal/voucher.html"},
		"p_forgot":       {"base.html", "portal/forgot.html"},
		"p_page":         {"base.html", "portal/page.html"},
		"p_payment":      {"base.html", "portal/layout.html", "portal/payment.html"},
		"p_activation":   {"base.html", "portal/layout.html", "portal/activation.html"},
		"p_friend":       {"base.html", "portal/layout.html", "portal/friend.html"},
		"p_forgot_user":  {"base.html", "portal/forgot_user.html"},
		"bulk_recharge":  {"base.html", "admin/app.html", "admin/bulk_recharge.html"},
		"admin_password": {"base.html", "admin/app.html", "admin/admin_password.html"},
		"setup":          {"base.html", "admin/app.html", "admin/setup.html", "admin/formfields.html", "admin/customer_pick.html", "admin/tplvars.html"},
	}
	s.templates = map[string]*template.Template{}
	for name, files := range pages {
		for i, f := range files {
			files[i] = path.Join("web/templates", f)
		}
		t, err := template.New("").Funcs(funcs).ParseFS(gobill.FS, files...)
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
	files, err := fs.Glob(gobill.FS, "web/static/icons/*.svg")
	if err != nil {
		return nil, err
	}
	icons := map[string]template.HTML{}
	for _, f := range files {
		b, err := fs.ReadFile(gobill.FS, f)
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
