// Package view holds the template helpers and number formatting of the web pages.
package view

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	gobill "github.com/frand-kod/gobill"
	moneyfmt "github.com/frand-kod/gobill/internal/money"
)

func HumanBytes(n int64) string {
	num, unit := HumanBytesParts(n)
	return num + " " + unit
}

// HumanBytesParts is HumanBytes split for the big-number style: ("112.61", "GB"), ("512", "B").
func HumanBytesParts(n int64) (string, string) {
	f, u := float64(n), "B"
	for _, next := range []string{"KB", "MB", "GB", "TB"} {
		if f < 1024 {
			break
		}
		f, u = f/1024, next
	}
	if u == "B" {
		return fmt.Sprintf("%d", n), u
	}
	return fmt.Sprintf("%.2f", f), u
}

func HumanDur(sec int64) string {
	d := time.Duration(max(sec, 0)) * time.Second
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", d/(24*time.Hour), d%(24*time.Hour)/time.Hour)
	}
	return fmt.Sprintf("%dh %02dm", d/time.Hour, d%time.Hour/time.Minute)
}

func Bps(n int64, unit string) string {
	if unit == "Kbps" {
		return strconv.FormatInt(n*1000, 10)
	}
	return strconv.FormatInt(n*1000000, 10)
}

// thousandsSep is the digit group separator used by Money. Settings load and save keep it current.
var thousandsSep atomic.Value // string

func SetThousandsSep(v string) {
	if v == "" {
		v = "."
	}
	thousandsSep.Store(v)
}

func GroupSep() string {
	if v, ok := thousandsSep.Load().(string); ok {
		return v
	}
	return "."
}

// Money formats rupiah with the configured thousands separator: Rp 1.234.000, -Rp 1.000.
func Money(n int64) string { return moneyfmt.Format(n, GroupSep()) }

// Badge maps a status value to its Badge colour class.
func Badge(v string) string {
	switch strings.ToLower(v) {
	case "active", "enable", "enabled", "unused", "yes", "online", "ok", "accepted":
		return "badge-ok"
	case "disable", "disabled", "banned", "suspended", "expired", "inactive", "no", "offline", "error", "rejected":
		return "badge-bad"
	case "limited", "secret lemah":
		return "badge-warn"
	}
	return "badge-muted"
}

// NavActive returns the entry of keys that owns path: the longest key equal to path or one of its parents.
// "/admin" only matches itself, otherwise the dashboard would be active on every admin page.
// Returns "" when no key matches.
func NavActive(path string, keys ...string) string {
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

// LoadIcons reads web/static/icons/*.svg and marks each icon decorative. The license comment
// stays in the markup: the ISC license needs its notice in every copy of the icon.
func LoadIcons() (map[string]template.HTML, error) {
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
		svg := bytes.Replace(b, []byte("<svg"), []byte(`<svg aria-hidden="true"`), 1)
		icons[strings.TrimSuffix(path.Base(f), ".svg")] = template.HTML(svg)
	}
	return icons, nil
}

// BadgeDot is the colour of a status dot for a Badge class from Badge().
func BadgeDot(badgeClass string) string {
	switch badgeClass {
	case "badge-ok":
		return "bg-ok-fg"
	case "badge-bad":
		return "bg-err-fg"
	case "badge-warn":
		return "bg-warn-fg"
	}
	return "bg-ink-2"
}

// Dict builds the map a sub-template takes several arguments through: {{template "x" dict "A" 1 "B" .Y}}
func Dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

// Funcs are the template functions that need no Server; the web package adds T, TT, asset and ts.
func Funcs() (template.FuncMap, error) {
	icons, err := LoadIcons()
	if err != nil {
		return nil, err
	}
	return template.FuncMap{
		"hasPrefix": strings.HasPrefix,
		"dict":      Dict,
		"navActive": NavActive,
		"money":     Money,
		"badge":     Badge,
		"dot":       func(v string) string { return BadgeDot(Badge(v)) },
		"icon": func(name string) (template.HTML, error) {
			svg, ok := icons[name]
			if !ok {
				return "", fmt.Errorf("unknown icon %q", name)
			}
			return svg, nil
		},
	}, nil
}
