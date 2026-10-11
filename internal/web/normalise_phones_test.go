package web

import (
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// After the startup fix-up, a customer saved as "0812-3456-789" is found by the admin search typed as
// the national form, and the list shows the stored number in the canonical form only.
func TestNormalisedPhoneFoundBySearch(t *testing.T) {
	s, q := newTestApp(t)
	ctx := t.Context()
	if _, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "budi", PasswordHash: "h", Fullname: "Budi",
		Phone: "0812-3456-789", ServiceType: "Others", Status: "Active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NormalisePhones(ctx, s.conn); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	cookie := login(t, h, "alice")
	w := do(h, "GET", "/admin/customers?q="+url.QueryEscape("08123456789"), nil, cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "budi") {
		t.Fatalf("search by 08123456789: status %d, budi listed: %v", w.Code, strings.Contains(w.Body.String(), "budi"))
	}
}
