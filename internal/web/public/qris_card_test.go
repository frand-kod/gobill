package public

import (
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/payment"
)

// When the company name equals the merchant name (any case), the card gives the short check only.
func TestQRISCardShortCheck(t *testing.T) {
	s := testHandlers(t)
	short := string(s.qrisCard(map[string]string{"company_name": " contoh qris "}, payment.QRISDetailsOf(isoQRIS), "", "Rp 1", "", true))
	if !strings.Contains(short, "Pastikan aplikasi Anda menampilkan contoh qris sebelum membayar.") || strings.Contains(short, "milik") {
		t.Error("short check missing or long check shown")
	}
	if !strings.Contains(short, "Contoh QR, tidak bisa dibayar") {
		t.Error("sample note missing")
	}
	if live := string(s.qrisCard(map[string]string{"company_name": "Toko"}, payment.QRISDetailsOf(isoQRIS), "", "Rp 1", "", false)); strings.Contains(live, "Contoh QR") {
		t.Error("sample note shown on a live code")
	}
}

// Missing merchant fields leave out their rows; NMID and provider show when present.
func TestQRISCardDetailsOptional(t *testing.T) {
	s := testHandlers(t)
	empty := string(s.qrisCard(map[string]string{"company_name": "Toko"}, payment.QRISDetails{}, "", "Rp 1", "", false))
	if strings.Contains(empty, "NMID") || strings.Contains(empty, "Penyedia") {
		t.Error("empty details still show rows")
	}
	full := string(s.qrisCard(map[string]string{"company_name": "Toko"}, payment.QRISDetails{MerchantName: "Warung", NMID: "ID999", Provider: "GoPay", City: "Bandung"}, "", "Rp 1", "", false))
	for _, want := range []string{"NMID", "ID999", "GoPay", "Bandung", "Warung"} {
		if !strings.Contains(full, want) {
			t.Errorf("card misses %q", want)
		}
	}
}
