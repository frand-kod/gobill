package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/web/view"

	"github.com/frand-kod/gobill/internal/db"
)

// A balance transfer names the recipient: the form looks the username up, and the flash says who got the money.
func TestPortalTransferNamesRecipient(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 5000)
	if _, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: "h", Fullname: "Two", ServiceType: "PPPoE", Status: "Active"}); err != nil {
		t.Fatal(err)
	}
	setting(t, e, "allow_balance_transfer", "yes")
	c, _ := custLogin(t, e, "u1", "pw12345")

	if w := do(e.h, "GET", "/portal/transfer/lookup?username=u2", nil, c); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Two"`) {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	if w := do(e.h, "GET", "/portal/transfer/lookup?username=nobody", nil, c); w.Code != http.StatusNotFound {
		t.Fatalf("unknown username lookup: %d, want 404", w.Code)
	}
	b := do(e.h, "GET", "/portal/profile", nil, c).Body.String()
	for _, want := range []string{`id="transfer"`, `data-lookup="/portal/transfer/lookup"`, `data-confirm-named="Kirim Rp {balance} ke {name} ({username})?"`} {
		if !strings.Contains(b, want) {
			t.Errorf("profile misses %q", want)
		}
	}

	if w := do(e.h, "POST", "/portal/transfer", url.Values{"username": {"u2"}, "balance": {"1000"}}, c); w.Code != http.StatusSeeOther {
		t.Fatalf("transfer: %d", w.Code)
	}
	if b := do(e.h, "GET", "/portal/profile", nil, c).Body.String(); !strings.Contains(b, "Saldo "+view.Money(1000)+" terkirim ke Two (u2)") {
		t.Fatal("flash does not name the recipient")
	}

	setting(t, e, "allow_balance_transfer", "no")
	if w := do(e.h, "GET", "/portal/transfer/lookup?username=u2", nil, c); w.Code != http.StatusNotFound {
		t.Fatalf("lookup with transfers off: %d, want 404", w.Code)
	}
}

// The payment check waits 30 seconds per payment; the message gets the seconds left.
func TestPayCheckWaitSeconds(t *testing.T) {
	s := &Server{}
	if w := s.payCheckWait(1); w != 0 {
		t.Fatalf("first check held back for %d s", w)
	}
	if w := s.payCheckWait(1); w < 25 || w > 30 {
		t.Fatalf("second check waits %d s, want the seconds left of the 30 s gap", w)
	}
	if w := s.payCheckWait(2); w != 0 {
		t.Fatalf("another payment held back for %d s", w)
	}
	s.payCheckMu.Lock()
	s.payCheckAt[1] = time.Now().Add(-payCheckGap)
	s.payCheckMu.Unlock()
	if w := s.payCheckWait(1); w != 0 {
		t.Fatalf("check after the gap held back for %d s", w)
	}
}

// Each login page has its own title: the admin page and the customer page are told apart in tabs and history.
func TestLoginPageTitles(t *testing.T) {
	e := billApp(t)
	for path, want := range map[string]string{"/admin/login": "<title>Masuk Admin - ", "/login": "<title>Masuk Pelanggan - "} {
		if b := do(e.h, "GET", path, nil, nil).Body.String(); !strings.Contains(b, want) {
			t.Errorf("%s: title misses %q", path, want)
		}
	}
}

// Negative money keeps the sign in front of the currency; the usage chart labels days in ISO form.
func TestMoneyNegativeAndDayLabels(t *testing.T) {
	if got, want := view.Money(-1000), "-"+view.Money(1000); got != want {
		t.Fatalf("money(-1000) = %q, want %q", got, want)
	}
	loc := time.UTC
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, loc).Unix()
	days := dailyUsage(nil, 7, now, loc)
	if days[0].Label != "2026-10-06" || days[6].Label != "2026-10-12" {
		t.Fatalf("day labels %q .. %q", days[0].Label, days[6].Label)
	}
}

// The lookup answers any signed-in customer, so it gives a masked name only.
func TestMaskName(t *testing.T) {
	for in, want := range map[string]string{"Budi Santoso": "Budi S.", "Siti Nur Aisyah": "Siti N. A.", "Two": "Two", "": ""} {
		if got := maskName(in); got != want {
			t.Errorf("maskName(%q) = %q, want %q", in, got, want)
		}
	}
}
