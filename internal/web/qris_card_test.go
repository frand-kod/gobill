package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

// The public QRIS page shows the card: company, merchant, amount and the payee check. The uploaded
// QRIS image is never referenced.
func TestQRISPageCard(t *testing.T) {
	s, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"qris_payload": isoQRIS, "company_name": "Toko Test", "phone": "081234567890"})
	tr, err := q.CreateTransaction(t.Context(), db.CreateTransactionParams{Invoice: "INV-1", Username: "budi",
		PlanName: "Paket 10M", Type: "Hotspot", Price: 150000, Method: "Cash"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/qris/%d/%s", tr.ID, payment.QRISToken(s.SecretKey, tr.ID))
	body := isoReq(h, "GET", path, "198.51.100.9", nil).Body.String()
	for _, want := range []string{"Pembayaran ke Toko Test", "CONTOH QRIS", "Contoh", money(150000), "Nominal sudah terkunci",
		"INV-1 · Paket 10M", "Scan dengan aplikasi bank atau e-wallet", "data:image/png;base64,",
		"Aplikasi Anda akan menampilkan nama merchant CONTOH QRIS", "milik Toko Test", "Simpan / cetak", "window.print()"} {
		if !strings.Contains(body, want) {
			t.Errorf("card misses %q", want)
		}
	}
	if strings.Contains(body, "/uploads/") {
		t.Error("the card references an uploaded file")
	}
}

// When the company name equals the merchant name (any case), the card gives the short check only.
func TestQRISCardShortCheck(t *testing.T) {
	s, _, _ := isoSetup(t)
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
	s, _, _ := isoSetup(t)
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
