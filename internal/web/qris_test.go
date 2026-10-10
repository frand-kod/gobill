package web

import (
	"testing"

	"github.com/skip2/go-qrcode"
)

// A QRIS photo decodes back to its text, and anything else is refused.
func TestQRISFromImage(t *testing.T) {
	want := settingSample["qris_payload"]
	png, err := qrcode.Encode(want, qrcode.Medium, 256)
	if err != nil {
		t.Fatal(err)
	}
	got, err := qrisFromImage(png)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	if _, err := qrisFromImage([]byte("not an image")); err == nil {
		t.Fatal("garbage accepted")
	}
	bad, _ := qrcode.Encode("https://example.com", qrcode.Medium, 256)
	if _, err := qrisFromImage(bad); err == nil {
		t.Fatal("non-QRIS QR accepted")
	}
}
