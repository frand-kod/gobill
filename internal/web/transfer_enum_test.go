package web

import (
	"net/url"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// Past the lookup cap, transfer and send-to-friend refuse the same way for a real and an unknown user.
func TestTransferFriendLookupCapped(t *testing.T) {
	e := billApp(t)
	p := e.plan(t, "Gold", "PPPoE", 1000)
	portalCust(t, e, 5000)
	e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Two", ServiceType: "PPPoE", Status: "Active"})
	c, _ := custLogin(t, e, "u1", "pw12345")
	for i := 0; i < 30; i++ {
		do(e.h, "POST", "/portal/transfer", url.Values{"username": {"nobody"}, "balance": {"100"}}, c)
	}
	path := "/portal/plans/" + itoa(p.ID) + "/friend"
	for _, u := range []string{"u2", "nobody"} {
		a := do(e.h, "POST", "/portal/transfer", url.Values{"username": {u}, "balance": {"100"}}, c)
		b := do(e.h, "POST", path, url.Values{"username": {u}}, c)
		if a.Code != 429 || b.Code != 429 {
			t.Fatalf("%s: transfer %d friend %d, want 429", u, a.Code, b.Code)
		}
	}
}
