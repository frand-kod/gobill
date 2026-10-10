package web

import (
	"fmt"
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
		for _, s := range []string{"--bg", "--surface", "--surface-2", "--field"} {
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

// Custom accent tone. These formulas must stay identical to the inline script in web/templates/base.html
// (lin, toLab, fromLab, toGamut, toneDark, lightAccent, onAccent, shade).

func (c rgb) hex() string {
	return fmt.Sprintf("#%02x%02x%02x", int(math.Floor(c[0]+0.5)), int(math.Floor(c[1]+0.5)), int(math.Floor(c[2]+0.5)))
}

func lin(x float64) float64 {
	x /= 255
	if x <= 0.04045 {
		return x / 12.92
	}
	return math.Pow((x+0.055)/1.055, 2.4)
}

// toLab is sRGB to OKLab (L, a, b).
func toLab(c rgb) [3]float64 {
	r, g, b := lin(c[0]), lin(c[1]), lin(c[2])
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return [3]float64{
		0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

// fromLab is OKLab to linear sRGB.
func fromLab(L, a, b float64) [3]float64 {
	l := L + 0.3963377774*a + 0.2158037573*b
	m := L - 0.1055613458*a - 0.0638541728*b
	s := L - 0.0894841775*a - 1.2914855480*b
	l, m, s = l*l*l, m*m*m, s*s*s
	return [3]float64{
		4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s,
	}
}

// toGamut turns OKLCH into a hex colour, lowering chroma until it is inside sRGB.
func toGamut(L, C, h float64) string {
	inside := func(v [3]float64) bool {
		for _, x := range v {
			if x < -1e-4 || x > 1+1e-4 {
				return false
			}
		}
		return true
	}
	lo, hi := 0.0, C
	for i := 0; i < 24; i++ {
		mid := (lo + hi) / 2
		if inside(fromLab(L, mid*math.Cos(h), mid*math.Sin(h))) {
			lo = mid
		} else {
			hi = mid
		}
	}
	v := fromLab(L, lo*math.Cos(h), lo*math.Sin(h))
	var out rgb
	for i, x := range v {
		if x <= 0.0031308 {
			x = 12.92 * x
		} else {
			x = 1.055*math.Pow(math.Max(x, 0), 1/2.4) - 0.055
		}
		out[i] = math.Min(1, math.Max(0, x)) * 255
	}
	return out.hex()
}

// toneDark: dark-mode accent. L clamped to 0.62..0.74, chroma capped at 0.14, hue kept.
func toneDark(hex string) string {
	lab := toLab(hexRGB(hex))
	return toGamut(math.Min(math.Max(lab[0], 0.62), 0.74), math.Min(math.Hypot(lab[1], lab[2]), 0.14), math.Atan2(lab[2], lab[1]))
}

// lightAccent: light-mode accent, the chosen colour unless neither white nor ink reaches 4.5:1 (then L is lowered).
func lightAccent(hex string) string {
	lab := toLab(hexRGB(hex))
	C, H := math.Hypot(lab[1], lab[2]), math.Atan2(lab[2], lab[1])
	out := hex
	for L := lab[0]; math.Max(contrast(hexRGB(out), white), contrast(hexRGB(out), ink)) < 4.5 && L > 0; L -= 0.01 {
		out = toGamut(L, C, H)
	}
	return out
}

func onAccent(hex string) string {
	if c := hexRGB(hex); contrast(c, white) >= contrast(c, ink) {
		return "#ffffff"
	}
	return "#111418"
}

// shade is the first step from c towards k (p = 1 down to 0 in 0.05 steps) whose contrast on bg reaches min.
func shade(c, k, bg rgb, min float64) string {
	for p := 1.0; p > 0; p -= 0.05 {
		if m := mixPct(c, k, p); contrast(m, bg) >= min {
			return m.hex()
		}
	}
	return k.hex()
}

func TestAccentToneDark(t *testing.T) {
	darkSurfaces := []rgb{hexRGB("#171b21"), hexRGB("#1e232a"), hexRGB("#0f1216")}
	lightSurfaces := []rgb{hexRGB("#f9fafb"), hexRGB("#eceef2")}
	const tol = 0.005 // 8-bit hex quantisation of L and C
	for _, in := range []string{"#01fafe", "#ffff00", "#ff00ff", "#00ff00", "#0000ff", "#ac0202", "#777777"} {
		light := lightAccent(in)
		dark := toneDark(in)
		lab := toLab(hexRGB(dark))
		C := math.Hypot(lab[1], lab[2])
		inLab := toLab(hexRGB(in))
		t.Logf("%s before: L %.3f C %.3f | after dark %s L %.3f C %.3f | light %s", in, inLab[0], math.Hypot(inLab[1], inLab[2]), dark, lab[0], C, light)

		if lab[0] < 0.62-tol || lab[0] > 0.74+tol {
			t.Errorf("%s: dark L %.3f outside 0.62..0.74", in, lab[0])
		}
		if C > 0.14+tol {
			t.Errorf("%s: dark chroma %.3f > 0.14", in, C)
		}
		if c := contrast(hexRGB(onAccent(light)), hexRGB(light)); c < 4.5 {
			t.Errorf("%s: light on-accent contrast %.2f < 4.5", in, c)
		}
		if c := contrast(hexRGB(onAccent(dark)), hexRGB(dark)); c < 4.5 {
			t.Errorf("%s: dark on-accent contrast %.2f < 4.5", in, c)
		}
		// accent text: light is the chosen colour shaded towards black, dark the toned one towards white
		fgL := shade(hexRGB(light), black, lightSurfaces[1], 5)
		fgD := shade(hexRGB(dark), white, darkSurfaces[0], 5)
		for _, s := range lightSurfaces {
			if c := contrast(hexRGB(fgL), s); c < 4.5 {
				t.Errorf("%s: light accent text %s on %s = %.2f", in, fgL, s.hex(), c)
			}
		}
		for _, s := range darkSurfaces {
			if c := contrast(hexRGB(fgD), s); c < 4.5 {
				t.Errorf("%s: dark accent text %s on %s = %.2f", in, fgD, s.hex(), c)
			}
		}
		// before: the old script used the chosen colour in both modes
		oldFgD := shade(hexRGB(in), white, darkSurfaces[0], 5)
		t.Logf("%s contrast: on-accent light %.2f dark %.2f (before dark %.2f) | text light %s %.2f dark %s %.2f (before dark %s %.2f)",
			in, contrast(hexRGB(onAccent(light)), hexRGB(light)), contrast(hexRGB(onAccent(dark)), hexRGB(dark)),
			contrast(hexRGB(onAccent(in)), hexRGB(in)),
			fgL, contrast(hexRGB(fgL), lightSurfaces[1]), fgD, contrast(hexRGB(fgD), darkSurfaces[0]),
			oldFgD, contrast(hexRGB(oldFgD), darkSurfaces[0]))
	}
}
