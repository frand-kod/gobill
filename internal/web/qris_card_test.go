package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/web/view"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/qris"
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
	path := fmt.Sprintf("/qris/%d/%s", tr.ID, qris.Token(s.SecretKey, tr.ID))
	body := isoReq(h, "GET", path, "198.51.100.9", nil).Body.String()
	for _, want := range []string{"Pembayaran ke Toko Test", "CONTOH QRIS", "Contoh", view.Money(150000), "Nominal sudah terkunci",
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
