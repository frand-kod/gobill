package notify

import "testing"

func TestFillNeverLeavesPlaceholders(t *testing.T) {
	n := &Notifier{Settings: map[string]string{"app_url": "https://bill.example/"}}
	got := n.fill("[[name]] [[price]] [[unknown]] [[payment_link]] [[invoice_link]]",
		map[string]string{"name": "Ann", "price": Money(1234000), "invoice_link": "/portal/orders/7/invoice"})
	want := "Ann Rp 1.234.000  https://bill.example/portal/plans https://bill.example/portal/orders/7/invoice"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// without app_url a relative link is useless in a WhatsApp message: empty
	n.Settings["app_url"] = ""
	if got := n.fill("[[payment_link]]|[[x]]", nil); got != "|" {
		t.Errorf("got %q", got)
	}
}
