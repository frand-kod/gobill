package payment

import (
	"strings"
	"testing"
)

const staticQRIS = "00020101021126610014COM.GO-JEK.WWW01189360091431538383250210G1538383250303UMI51440014ID.CO.QRIS.WWW0215ID10264879603990303UMI5204481453033605802ID59164 Keys Solutions6010YOGYAKARTA61055516162140703A0111036216304BA80"

func TestQRIS(t *testing.T) {
	if err := QRISValid(staticQRIS); err != nil {
		t.Fatal(err)
	}
	if err := QRISValid(strings.Replace(staticQRIS, "BA80", "BA81", 1)); err == nil {
		t.Fatal("bad CRC accepted")
	}
	got, err := QRISAmount(staticQRIS, 165000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "010212") || !strings.Contains(got, "5406165000"+"5802ID") {
		t.Fatal(got)
	}
	if err := QRISValid(got); err != nil {
		t.Fatal(err, got)
	}
	if _, err := QRISAmount(staticQRIS, 0); err == nil {
		t.Fatal("zero amount accepted")
	}
}

func TestQRISToken(t *testing.T) {
	a, b := QRISToken([]byte("k"), 7), QRISToken([]byte("k"), 8)
	if len(a) != 16 || a == b || a != QRISToken([]byte("k"), 7) {
		t.Fatal(a, b)
	}
}
