package public

import "testing"

func TestWaCommandText(t *testing.T) {
	for in, want := range map[string]string{"#cek": "cek", "/bayar": "bayar", " ! putus budi ": "putus budi", "123456": "123456", "#1": "1"} {
		if got, ok := waCommandText(in); !ok || got != want {
			t.Errorf("%q: got %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"halo", "cek", "1", "#", "1234567", "12ab", ""} {
		if _, ok := waCommandText(in); ok {
			t.Errorf("%q: want ignored", in)
		}
	}
}
