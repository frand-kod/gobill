package web

import (
	"fmt"
	"github.com/frand-kod/gobill/internal/web/view"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/payment"
)

// A disabled account gets the specific message only after the right password; a wrong one stays generic.
func TestPortalLoginDisabledMessage(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	if _, err := e.s.conn.Exec("UPDATE customers SET status = 'Disabled'"); err != nil {
		t.Fatal(err)
	}
	const msg = "Akun tidak dapat digunakan, hubungi admin"
	if b := do(e.h, "POST", "/login", url.Values{"username": {"u1"}, "password": {"salah"}}, nil).Body.String(); strings.Contains(b, msg) {
		t.Error("wrong password reveals the account state")
	}
	if b := do(e.h, "POST", "/login", url.Values{"username": {"u1"}, "password": {"pw12345"}}, nil).Body.String(); !strings.Contains(b, msg) {
		t.Error("disabled account message missing after the right password")
	}
}

// The balance button names how much is missing.
func TestPortalPlansShortfall(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 20000)
	e.plan(t, "Gold", "PPPoE", 50000)
	c, _ := custLogin(t, e, "u1", "pw12345")
	b := do(e.h, "GET", "/portal/plans", nil, c).Body.String()
	for _, want := range []string{"Kurang " + view.Money(30000), `aria-describedby="sf`, "1 Hari", "Internet rumah"} {
		if !strings.Contains(b, want) {
			t.Errorf("plans page misses %q", want)
		}
	}
}

// A QRIS page always shows the QR (an overdue bill must stay payable), the confirmation note and a way back.
func TestQRISPageNotPending(t *testing.T) {
	s, h, q := isoSetup(t)
	isoSettings(t, q, map[string]string{"qris_payload": isoQRIS, "company_name": "Toko"})
	mk := func(end int64) string {
		tr, err := q.CreateTransaction(t.Context(), db.CreateTransactionParams{Invoice: fmt.Sprint("INV-", end), Username: "budi",
			PlanName: "P", Type: "Hotspot", Price: 1000, Method: "Cash", PeriodEnd: end})
		if err != nil {
			t.Fatal(err)
		}
		return isoReq(h, "GET", fmt.Sprintf("/qris/%d/%s", tr.ID, payment.QRISToken(s.SecretKey, tr.ID)), "198.51.100.9", nil).Body.String()
	}
	if b := mk(time.Now().Add(-time.Hour).Unix()); !strings.Contains(b, "data:image/png") {
		t.Error("overdue invoice lost its QR")
	}
	if b := mk(time.Now().Add(time.Hour).Unix()); !strings.Contains(b, "data:image/png") || !strings.Contains(b, "Kembali ke portal") || !strings.Contains(b, "memeriksa") {
		t.Error("live invoice misses the QR, the note or the back link")
	}
}
