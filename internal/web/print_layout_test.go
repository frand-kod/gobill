package web

import (
	"strings"
	"testing"
)

// The print page takes ?layout=58|80 for thermal printers; anything else is A4.
func TestVoucherPrintLayout(t *testing.T) {
	_, h, _, c := crudApp(t)
	for q, want := range map[string]string{"": "l-a4", "?layout=58": "size: 58mm", "?layout=80": "size: 80mm", "?layout=999": "l-a4"} {
		body := do(h, "GET", "/admin/vouchers/print"+q, nil, c).Body.String()
		if !strings.Contains(body, want) {
			t.Errorf("print%s: want %q in page", q, want)
		}
		if q == "?layout=999" && strings.Contains(body, "@page") {
			t.Errorf("invalid layout must fall back to A4")
		}
	}
}
