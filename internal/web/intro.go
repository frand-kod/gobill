package web

import (
	"regexp"
	"strings"
)

// intro is the short "what is this page" callout shown above the content. Title, Text and Tip are
// English catalog keys (translated in render); Tip is only set where there is a common pitfall.
type intro struct{ Title, Text, Tip string }

var numSeg = regexp.MustCompile(`/\d+`)

// introKey turns a request path into the key of the intro tables: ids, "/new" and "/edit" are dropped.
func introKey(p string) string {
	p = numSeg.ReplaceAllString(p, "")
	p = strings.TrimSuffix(strings.TrimSuffix(p, "/edit"), "/new")
	return strings.TrimSuffix(p, "/")
}

// introFor picks the intro of a page. List and form pages match the longest known path prefix;
// custom pages (dashboard, reports, ...) are keyed by their template name.
func introFor(tmpl, urlPath string) intro {
	switch tmpl {
	case "list", "form":
		k := introKey(urlPath)
		tbl := listIntros
		if tmpl == "form" {
			tbl = formIntros
		}
		for k != "" {
			if in, ok := tbl[k]; ok {
				return in
			}
			i := strings.LastIndex(k, "/")
			if i <= 0 {
				break
			}
			k = k[:i]
		}
		return intro{}
	case "report":
		if introKey(urlPath) == "/admin/reports/period" {
			return pageIntros["report_period"]
		}
	}
	return pageIntros[tmpl]
}

func (s *Server) translateIntro(in intro) intro {
	l := s.language()
	if in.Title == "" {
		return in
	}
	in.Title, in.Text = s.catalog.T(l, in.Title), s.catalog.T(l, in.Text)
	if in.Tip != "" {
		in.Tip = s.catalog.T(l, in.Tip)
	}
	return in
}
