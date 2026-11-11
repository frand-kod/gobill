package core

// Page rendering and the error pages.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net/http"
)

// Fail logs err with a reference and answers the 500 page.
func (s *Core) Fail(w http.ResponseWriter, what string, err error) {
	ref := make([]byte, 4)
	rand.Read(ref)
	slog.Error(what, "err", err, "ref", hex.EncodeToString(ref))
	s.ErrorPage(w, hex.EncodeToString(ref))
}

// ErrorPage is the 500 page: Indonesian text from the catalog, no internal error text, a reference to find the log line.
func (s *Core) ErrorPage(w http.ResponseWriter, ref string) {
	s.ErrorHTML(w, http.StatusInternalServerError, "Something went wrong", "The request could not be completed. Check the data before trying again.", ref)
}

// LangCode is the html lang attribute for the active language.
func (s *Core) LangCode() string {
	switch s.Language() {
	case "english":
		return "en"
	case "arabic":
		return "ar"
	case "spanish":
		return "es"
	case "turkish":
		return "tr"
	}
	return "id"
}

// ErrorHTML is the shared error page (500, 403, 404, 405). It does not use the page layout because fail has
// no request (and the failure may be in the layout itself), so it carries its own lang and a small theme script.
func (s *Core) ErrorHTML(w http.ResponseWriter, code int, title, msg, ref string) {
	t := func(k string) string { return html.EscapeString(s.T(k)) }
	refLine := ""
	if ref != "" {
		refLine = `<p class="hint">Ref: ` + html.EscapeString(ref) + `</p>`
	}
	dir := "ltr"
	if s.Language() == "arabic" {
		dir = "rtl"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><html lang="%[6]s" dir="%[7]s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%[1]s</title>`+
		`<script>try{var t=JSON.parse(localStorage.getItem('gb-theme')||'{}'),m=t.mode||'system',d=document.documentElement;d.classList.toggle('dark',m==='dark'||m==='system'&&matchMedia('(prefers-color-scheme: dark)').matches);if(t.accent==='orange')d.dataset.accent='orange'}catch(e){}</script>`+
		`<link rel="stylesheet" href="%[5]s"></head>
<body><main class="mx-auto max-w-md p-4"><div class="card mt-12"><div class="card-body space-y-4" role="alert"><h1 class="text-xl font-semibold">%[1]s</h1><p>%[2]s</p>%[3]s
<a class="btn btn-primary min-h-10" href="/" onclick="history.back();return false">%[4]s</a></div></div></main></body></html>`,
		t(title), t(msg), refLine, t("Go back"), s.Asset("/static/app.css"), s.LangCode(), dir)
}
