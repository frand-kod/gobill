package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
)

// secondCust makes customer u2 (with a portal password) and returns its session cookie.
func secondCust(t *testing.T, e *payEnv) (db.Customer, *http.Cookie) {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte("pw12345"), bcrypt.MinCost)
	c2, err := e.q.CreateCustomer(t.Context(), db.CreateCustomerParams{Username: "u2", PasswordHash: string(h), Fullname: "U Two",
		Email: "u2@example.com", ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	ck, _ := custLogin(t, e.billEnv, "u2", "pw12345")
	if ck == nil {
		t.Fatal("u2 login")
	}
	return c2, ck
}

func TestPortalPaymentIDOR(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	_, other := secondCust(t, e)
	path := "/portal/payments/" + itoa(pr.ID)
	if w := do(e.h, "GET", path, nil, other); w.Code != 404 {
		t.Fatalf("view: %d", w.Code)
	}
	e.status = "PAID"
	if w := do(e.h, "POST", path+"/check", url.Values{}, other); w.Code != 404 {
		t.Fatalf("check: %d", w.Code)
	}
	if e.pending(t).Status != "pending" || e.trxCount(t) != 0 {
		t.Fatal("foreign check settled the payment")
	}
	if w := do(e.h, "GET", path, nil, e.cookie); w.Code != 200 {
		t.Fatalf("owner view: %d", w.Code)
	}
	if w := do(e.h, "GET", "/portal/payments/99999", nil, e.cookie); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
	// a customer deleted after ordering: nobody owns the row
	if _, err := e.s.conn.Exec("UPDATE payment_requests SET customer_id = NULL WHERE id = ?", pr.ID); err != nil {
		t.Fatal(err)
	}
	if w := do(e.h, "GET", path, nil, e.cookie); w.Code != 404 {
		t.Fatalf("orphan: %d", w.Code)
	}
}

func TestPortalPayCheckSettlesOnceAndSkipsClosed(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	e.status = "PAID"
	for range 3 { // double click / replay
		if c := do(e.h, "POST", "/portal/payments/"+itoa(pr.ID)+"/check", url.Values{}, e.cookie).Code; c != 303 {
			t.Fatalf("check: %d", c)
		}
	}
	if e.pending(t).Status != "paid" || e.trxCount(t) != 1 {
		t.Fatalf("status=%s trx=%d", e.pending(t).Status, e.trxCount(t))
	}
	// a second order is a separate payment; once expired it is not polled or credited
	e.order(t, url.Values{"channel": {"QRIS"}})
	rs, _ := e.q.SearchPaymentRequests(t.Context(), db.SearchPaymentRequestsParams{PageLimit: 10})
	if len(rs) != 2 {
		t.Fatalf("rows: %d", len(rs))
	}
	for _, r := range rs {
		if r.ID != pr.ID {
			e.s.conn.Exec("UPDATE payment_requests SET status = 'expired' WHERE id = ?", r.ID)
			if c := do(e.h, "POST", "/portal/payments/"+itoa(r.ID)+"/check", url.Values{}, e.cookie).Code; c != 303 {
				t.Fatalf("expired check: %d", c)
			}
		}
	}
	if e.trxCount(t) != 1 {
		t.Fatalf("expired row credited: trx=%d", e.trxCount(t))
	}
}

// A non-PAID callback never credits, and FAILED/EXPIRED after PAID neither undoes nor double-credits.
func TestTripayCallbackStatusTransitions(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	send := func(status string) {
		b := fmt.Sprintf(`{"merchant_ref":%q,"status":%q}`, pr.Ref, status)
		postCallback(e.h, b, signed(b))
	}
	for _, st := range []string{"UNPAID", "FAILED", "EXPIRED"} {
		send(st)
		if e.trxCount(t) != 0 || e.pending(t).PaidAt.Valid {
			t.Fatalf("%s credited", st)
		}
	}
	send("PAID")
	send("PAID")
	send("FAILED")
	send("EXPIRED")
	if got := e.pending(t); got.Status != "paid" || e.trxCount(t) != 1 {
		t.Fatalf("status=%s trx=%d", got.Status, e.trxCount(t))
	}
}

func TestTripayCallbackAmountMismatch(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	body := fmt.Sprintf(`{"merchant_ref":%q,"status":"PAID","total_amount":%d}`, pr.Ref, pr.Amount-1)
	if w := postCallback(e.h, body, signed(body)); w.Code != 400 {
		t.Fatalf("mismatch: %d", w.Code)
	}
	if e.pending(t).Status != "pending" || e.trxCount(t) != 0 {
		t.Fatal("mismatched amount credited")
	}
	// the fee charged to the customer is not part of the order amount
	body = fmt.Sprintf(`{"merchant_ref":%q,"status":"PAID","total_amount":%d,"fee_customer":750}`, pr.Ref, pr.Amount+750)
	if w := postCallback(e.h, body, signed(body)); w.Code != 200 {
		t.Fatalf("match: %d", w.Code)
	}
	if e.pending(t).Status != "paid" || e.trxCount(t) != 1 {
		t.Fatal("matching amount not credited")
	}
}

func TestContactTaken(t *testing.T) {
	e := payApp(t)
	c2, _ := secondCust(t, e)
	r := httptest.NewRequest("GET", "/", nil)
	if ok, err := e.s.contactTaken(r, "email", "U2@Example.com", e.cust.ID); err != nil || !ok {
		t.Fatalf("other's email: %v %v", ok, err)
	}
	if ok, _ := e.s.contactTaken(r, "email", "u2@example.com", c2.ID); ok {
		t.Fatal("own email counted as taken")
	}
	if ok, _ := e.s.contactTaken(r, "email", "free@example.com", e.cust.ID); ok {
		t.Fatal("free email taken")
	}
	// phones are only unique when wa_unique_per_account = yes
	if err := e.q.UpdateCustomer(t.Context(), db.UpdateCustomerParams{Fullname: "U Two", Phone: "081300000000", Email: "u2@example.com",
		ServiceType: "PPPoE", AutoRenewal: 1, Status: "Active", ID: c2.ID}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.s.contactTaken(r, "phone", "081300000000", e.cust.ID); ok {
		t.Fatal("phone unique without the setting")
	}
	setting(t, e.billEnv, "wa_unique_per_account", "yes")
	if ok, _ := e.s.contactTaken(r, "phone", "6281300000000", e.cust.ID); !ok {
		t.Fatal("phone not detected")
	}
	if ok, _ := e.s.contactTaken(r, "phone", "081300000000", c2.ID); ok {
		t.Fatal("own phone taken")
	}
}

// After otpTries wrong codes the flow is dropped: the right code no longer verifies and the phone is unchanged.
func TestContactOTPBruteForceLimit(t *testing.T) {
	e, otp := forgotApp(t)
	setting(t, e, "allow_phone_otp", "yes")
	ck, _ := custLogin(t, e, "u1", "pw12345")
	if ck == nil {
		t.Fatal("login")
	}
	w := do(e.h, "POST", "/portal/contact/phone/otp", url.Values{"value": {"081399999999"}}, ck)
	if w.Code != 303 {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	good := otp()
	wrong := "000000"
	if good == wrong {
		wrong = "111111"
	}
	for i := 1; i <= otpTries; i++ {
		w = do(e.h, "POST", "/portal/contact/phone/verify", url.Values{"otp": {wrong}}, ck)
		if w.Code != 200 || strings.Contains(w.Body.String(), "Data Saved") {
			t.Fatalf("wrong #%d: %d", i, w.Code)
		}
	}
	if w = do(e.h, "POST", "/portal/contact/phone/verify", url.Values{"otp": {good}}, ck); w.Code == 303 {
		t.Fatal("correct code accepted after the attempt limit")
	}
	if c, _ := e.q.GetCustomer(t.Context(), e.cust.ID); c.Phone != "081234567890" {
		t.Fatalf("phone changed to %s", c.Phone)
	}
}
