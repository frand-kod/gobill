package payment

import (
	"fmt"
	"strings"
	"testing"
)

const staticQRIS = "00020101021126520016ID.CO.CONTOH.WWW01129360000000010212ID10000000015204549953033605802ID5911CONTOH QRIS6006CONTOH9954XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX63048687"

func TestQRIS(t *testing.T) {
	if err := QRISValid(staticQRIS); err != nil {
		t.Fatal(err)
	}
	if err := QRISValid(strings.Replace(staticQRIS, "8687", "8688", 1)); err == nil {
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

// QRISDetailsOf reads the merchant fields and maps the provider domain, falling back to the domain.
func TestQRISDetailsOf(t *testing.T) {
	d := QRISDetailsOf(staticQRIS)
	if d.MerchantName != "CONTOH QRIS" || d.City != "CONTOH" || d.Provider != "Contoh" || d.NMID != "" {
		t.Fatalf("dummy: %+v", d)
	}
	if d := QRISDetailsOf("not a payload"); d != (QRISDetails{}) {
		t.Fatalf("invalid input: %+v", d)
	}
	// a GO-JEK domain in tag 26 is mapped to its name
	merchant := tlvOf("00", "COM.GO-JEK.WWW", "02", "ID1234567890")
	s := tlvOf("00", "01", "26", merchant, "59", "TOKO", "60", "KOTA") + "6304"
	s += fmt.Sprintf("%04X", crc16(s))
	if d := QRISDetailsOf(s); d.Provider != "GoPay" || d.MerchantName != "TOKO" || d.City != "KOTA" {
		t.Fatalf("gopay: %+v", d)
	}
	if d := QRISDetailsOf(tlvOf("00", "01", "26", tlvOf("00", "ID.FOO.BAR"))); d.Provider != "ID.FOO.BAR" {
		t.Fatal("unknown domain not shown as is:", d)
	}
}

func TestQRISToken(t *testing.T) {
	a, b := QRISToken([]byte("k"), 7), QRISToken([]byte("k"), 8)
	if len(a) != 16 || a == b || a != QRISToken([]byte("k"), 7) {
		t.Fatal(a, b)
	}
}

// DummyQRIS has the requested length, a valid CRC and only the fake merchant data.
func TestDummyQRIS(t *testing.T) {
	if DummyQRIS(len(staticQRIS)) != staticQRIS {
		t.Fatal("staticQRIS is not DummyQRIS output")
	}
	for n := 130; n <= 400; n++ {
		s := DummyQRIS(n)
		if len(s) != n {
			t.Fatalf("length %d: got %d", n, len(s))
		}
		if err := QRISValid(s); err != nil {
			t.Fatalf("length %d: %v", n, err)
		}
	}
	if m, _ := QRISInfo(staticQRIS); m != "CONTOH QRIS" || !strings.Contains(staticQRIS, "CONTOH") {
		t.Fatal("not the fake merchant:", m)
	}
}
