package web

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// A pending payment is found again on the dashboard and the orders page until it expires or is paid.
func TestPortalShowsPendingPayment(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	link := "/portal/payments/" + itoa(pr.ID)
	for _, page := range []string{"/portal", "/portal/orders"} {
		if w := do(e.h, "GET", page, nil, e.cookie); !strings.Contains(w.Body.String(), link) {
			t.Fatalf("%s: pending payment link missing", page)
		}
	}
	if _, err := e.s.conn.Exec("UPDATE payment_requests SET expires_at = ? WHERE id = ?", time.Now().Unix()-1, pr.ID); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"/portal", "/portal/orders"} {
		if w := do(e.h, "GET", page, nil, e.cookie); strings.Contains(w.Body.String(), link) {
			t.Fatalf("%s: expired payment still shown", page)
		}
	}
}

// The payment page reloads itself only while pending, and every status offers its action.
func TestPaymentPageStopsReloading(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	get := func() string { return do(e.h, "GET", "/portal/payments/"+itoa(pr.ID), nil, e.cookie).Body.String() }
	if b := get(); !strings.Contains(b, "location.reload") {
		t.Fatal("pending page does not reload")
	}
	for status, action := range map[string]string{"paid": `href="/portal"`, "expired": `href="/portal/plans"`, "failed": `href="/portal/plans"`} {
		if _, err := e.s.conn.Exec("UPDATE payment_requests SET status = ? WHERE id = ?", status, pr.ID); err != nil {
			t.Fatal(err)
		}
		if b := get(); strings.Contains(b, "location.reload") || !strings.Contains(b, action) {
			t.Fatalf("%s: reloads=%v has action=%v", status, strings.Contains(b, "location.reload"), strings.Contains(b, action))
		}
	}
}

func TestOTPWait(t *testing.T) {
	if otpWait(1000, 1010) != 50 || otpWait(1000, 1060) != 0 || otpWait(1000, 2000) != 0 || otpWait(0, 1000) != 0 {
		t.Fatal("resend gap is not 60 seconds")
	}
}
