package billing

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/notify"
)

// A balance top-up sends invoice_balance (with the balance before and after), not the paid invoice,
// as Package.php #rechargeBalance does.
func TestBalanceTopUpSendsBalanceInvoice(t *testing.T) {
	ctx, e := context.Background(), setup(t)
	sent := make(chan string, 10)
	n := &notify.Notifier{Settings: map[string]string{
		"user_notification_payment": "sms",
		"wa_url":                    "http://127.0.0.1:1/?to=[number]&text=[text]",
		"notif_invoice_balance":     "Saldo [[balance_before]] -> [[balance]] [[invoice]] [[type]]",
		"notif_invoice_paid":        "PAID [[invoice]]",
	}, HTTP: http.DefaultClient, Timeout: 5 * time.Second,
		Log: func(ch, to, subject, body string, err error) { sent <- subject + "\n" + body }}
	e.s.Reload(n, jkt)
	if _, err := e.conn.ExecContext(ctx, "UPDATE customers SET phone = '081234567890' WHERE id = ?", e.cust.ID); err != nil {
		t.Fatal(err)
	}
	before := e.balance()
	if err := e.s.Deposit(ctx, e.cust.ID, 0, 7000, "cash in", 0); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-sent:
		want := "Saldo " + notify.Money(before) + " -> " + notify.Money(before+7000)
		if !strings.Contains(msg, want) || !strings.Contains(msg, "INV-") || !strings.Contains(msg, "Balance") {
			t.Fatalf("top-up message: %q, want %q", msg, want)
		}
		if strings.Contains(msg, "PAID") {
			t.Fatalf("the paid invoice went out on a top-up: %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no top-up message")
	}
	select {
	case msg := <-sent:
		t.Fatalf("a second message went out: %q", msg)
	case <-time.After(200 * time.Millisecond):
	}
}
