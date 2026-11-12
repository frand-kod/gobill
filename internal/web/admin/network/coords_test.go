package network

import (
	"testing"
)

func TestCoordsValidation(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		" -6.2 , 106.8 ":   "-6.2,106.8",
		"-90,180":          "-90,180",
		"-6.200000,106.81": "-6.2,106.81",
	} {
		if got, ok := checkCoords(in); !ok || got != want {
			t.Errorf("checkCoords(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"-6.2", "91,0", "0,181", "a,b", "NaN,1", "1,2,3", "1;2"} {
		if _, ok := checkCoords(in); ok {
			t.Errorf("checkCoords(%q) accepted", in)
		}
	}
}
