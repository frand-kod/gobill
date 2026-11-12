package adminx

import (
	"testing"

	"github.com/skip2/go-qrcode"
)

const sampleQRIS = "00020101021126520016ID.CO.CONTOH.WWW01129360000000010212ID10000000015204549953033605802ID5911CONTOH QRIS6006CONTOH9954XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX63048687"

// A QRIS photo decodes back to its text, and anything else is refused.
func TestQRISFromImage(t *testing.T) {
	want := sampleQRIS
	png, err := qrcode.Encode(want, qrcode.Medium, 256)
	if err != nil {
		t.Fatal(err)
	}
	got, err := QrisFromImage(png)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	if _, err := QrisFromImage([]byte("not an image")); err == nil {
		t.Fatal("garbage accepted")
	}
	bad, _ := qrcode.Encode("https://example.com", qrcode.Medium, 256)
	if _, err := QrisFromImage(bad); err == nil {
		t.Fatal("non-QRIS QR accepted")
	}
}
