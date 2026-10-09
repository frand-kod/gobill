package web

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// WCAG AA check of the theme tokens in web/tailwind.css. Presets hardcode --on-accent; the accent text colour
// (--accent-fg) is a color-mix of the accent with black (light) or white (dark), as in the stylesheet.

type rgb [3]float64

func hexRGB(h string) rgb {
	var c rgb
	for i := range c {
		v, _ := strconv.ParseUint(h[1+2*i:3+2*i], 16, 8)
		c[i] = float64(v)
	}
	return c
}

func (c rgb) lum() float64 {
	l := func(x float64) float64 {
		x /= 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*l(c[0]) + 0.7152*l(c[1]) + 0.0722*l(c[2])
}

func contrast(a, b rgb) float64 {
	x, y := a.lum(), b.lum()
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// mixPct is color-mix(in srgb, a p%, b).
func mixPct(a, b rgb, p float64) (c rgb) {
	for i := range c {
		c[i] = a[i]*p + b[i]*(1-p)
	}
	return
}

var (
	white = hexRGB("#ffffff")
	black = hexRGB("#000000")
	ink   = hexRGB("#111418") // --on-accent of the orange preset
)

func TestThemeContrastAA(t *testing.T) {
	css, err := os.ReadFile("../../web/tailwind.css")
	if err != nil {
		t.Fatal(err)
	}
	token := func(block, name string) rgb {
		t.Helper()
		m := regexp.MustCompile(block + `[^}]*?` + regexp.QuoteMeta(name) + `:\s*(#[0-9a-fA-F]{6})`).FindSubmatch(css)
		if m == nil {
			t.Fatalf("token %s not found in %s", name, block)
		}
		return hexRGB(string(m[1]))
	}
	lightTok := func(n string) rgb { return token(`:root \{`, n) }
	darkTok := func(n string) rgb { return token(`:root\.dark \{`, n) }
	check := func(what string, a, b rgb, min float64) {
		t.Helper()
		if c := contrast(a, b); c < min {
			t.Errorf("%s: contrast %.2f < %.1f", what, c, min)
		} else {
			t.Logf("%-44s %.2f", what, c)
		}
	}

	// surfaces and text, both modes
	for _, m := range []struct {
		name string
		tok  func(string) rgb
	}{{"light", lightTok}, {"dark", darkTok}} {
		for _, s := range []string{"--bg", "--surface", "--surface-2"} {
			check(m.name+" --text on "+s, m.tok("--text"), m.tok(s), 4.5)
			check(m.name+" --text-2 on "+s, m.tok("--text-2"), m.tok(s), 4.5)
		}
		for _, k := range []string{"ok", "warn", "err", "info"} {
			check(m.name+" "+k+" fg on bg", m.tok("--"+k+"-fg"), m.tok("--"+k+"-bg"), 4.5)
			check(m.name+" "+k+" fg on surface", m.tok("--"+k+"-fg"), m.tok("--surface"), 4.5)
		}
	}

	// accent presets
	presets := map[string]struct{ accent, on rgb }{
		"teal":   {lightTok("--accent"), lightTok("--on-accent")},
		"orange": {hexRGB(regexp.MustCompile(`data-accent="orange"\] \{ --accent: (#[0-9a-f]{6})`).FindStringSubmatch(string(css))[1]), ink},
	}
	for n, p := range presets {
		check(n+" button text on accent", p.on, p.accent, 4.5)
		hover := mixPct(p.accent, black, 0.88)
		check(n+" button text on hover", p.on, hover, 4.5)
		fgL := mixPct(p.accent, black, 0.6)
		for _, s := range []string{"--surface", "--bg"} {
			check(n+" light accent text on "+s, fgL, lightTok(s), 4.5)
		}
		check(n+" light active nav (9% tint)", fgL, mixPct(p.accent, lightTok("--surface"), 0.09), 4.5)
		fgD := mixPct(p.accent, white, 0.5)
		for _, s := range []string{"--surface", "--bg", "--surface-2"} {
			check(n+" dark accent text on "+s, fgD, darkTok(s), 4.5)
		}
		check(n+" dark active nav (14% tint)", fgD, mixPct(p.accent, darkTok("--surface"), 0.14), 4.5)
		check(n+" dark button text on accent", p.on, p.accent, 4.5)
	}
}
