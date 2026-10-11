package db

import "testing"

func TestCanonicalPhone(t *testing.T) {
	cases := map[string]string{
		"0812-3456-789":    "628123456789",
		"+62 812 3456 789": "628123456789",
		"628123456789":     "628123456789",
		"(0812) 3456.789":  "628123456789",
		"0":                "",
		"":                 "",
	}
	for in, want := range cases {
		if got := CanonicalPhone(in); got != want {
			t.Errorf("CanonicalPhone(%q) = %q, want %q", in, got, want)
		}
	}
}
