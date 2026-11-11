package money

import "testing"

func TestFormat(t *testing.T) {
	for _, c := range []struct {
		v       int64
		sep, ui string
		msg     string
	}{
		{0, ".", "Rp 0", "Rp 0"},
		{999, ".", "Rp 999", "Rp 999"},
		{1000, ".", "Rp 1.000", "Rp 1.000"},
		{1234567, ",", "Rp 1,234,567", "Rp 1.234.567"},
		{-1000, ".", "-Rp 1.000", "Rp -1.000"},
		{-100000, ".", "-Rp 100.000", "Rp -100.000"},
	} {
		if got := Format(c.v, c.sep); got != c.ui {
			t.Errorf("Format(%d, %q) = %q, want %q", c.v, c.sep, got, c.ui)
		}
		if got := Message(c.v); got != c.msg {
			t.Errorf("Message(%d) = %q, want %q", c.v, got, c.msg)
		}
	}
}
