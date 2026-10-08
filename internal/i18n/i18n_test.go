package i18n

import (
	"testing"
	"testing/fstest"
)

func TestT(t *testing.T) {
	c, err := Load(fstest.MapFS{
		"lang/indonesia.json": {Data: []byte(`{"Log_in":"Masuk","Password":"Kata Sandi"}`)},
	}, "lang")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"Password":  "Kata Sandi", // exact key
		"Log in":    "Masuk",      // sanitized key
		"Log   in":  "Masuk",      // whitespace collapsed
		"Not there": "Not there",  // fallback
	}
	for in, want := range cases {
		if got := c.T("indonesia", in); got != want {
			t.Errorf("T(%q) = %q, want %q", in, got, want)
		}
	}
	if got := c.T("missing-lang", "Password"); got != "Password" {
		t.Errorf("unknown language should fall back, got %q", got)
	}
}
