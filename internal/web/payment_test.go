package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/payment"
)

const tripayKey = "priv-secret-key"

type payEnv struct {
	*billEnv
	fake   *httptest.Server
	status string // what the fake Tripay detail endpoint reports
	cookie *http.Cookie
	plan   db.Plan
}

// payApp runs the app against a fake Tripay server with the gateway switched on.
func payApp(t *testing.T) *payEnv {
	e := &payEnv{billEnv: billApp(t), status: "UNPAID"}
	mux := http.NewServeMux()
	mux.HandleFunc("/merchant/payment-channel", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":[{"code":"QRIS","name":"QRIS","group":"E-Wallet","active":true},{"code":"OFF","name":"Off","group":"x","active":false}]}`)
	})
	mux.HandleFunc("/transaction/create", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"reference":"T123","checkout_url":"https://pay.example/T123"}}`)
	})
	mux.HandleFunc("/transaction/detail", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"success":true,"data":{"status":%q}}`, e.status)
	})
	e.fake = httptest.NewServer(mux)
	t.Cleanup(e.fake.Close)
	e.s.NewGateway = func(cfg map[string]string) (Gateway, error) {
		g, err := payment.NewTripay(cfg)
		if err == nil {
			g.BaseURL = e.fake.URL
		}
		return g, err
	}
	for k, v := range map[string]string{"payment_gateway": "tripay", "tripay_api_key": "k", "tripay_private_key": tripayKey, "tripay_merchant_code": "M1"} {
		if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	portalCust(t, e.billEnv, 0)
	e.cookie, _ = custLogin(t, e.billEnv, "u1", "pw12345")
	e.plan = e.plan0(t)
	return e
}

func (e *payEnv) plan0(t *testing.T) db.Plan { return e.billEnv.plan(t, "Home", "PPPoE", 100000) }

func (e *payEnv) order(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	return do(e.h, "POST", "/portal/plans/"+itoa(e.plan.ID)+"/pay", form, e.cookie)
}

func signed(body string) string {
	h := hmac.New(sha256.New, []byte(tripayKey))
	h.Write([]byte(body))
	return hex.EncodeToString(h.Sum(nil))
}

func postCallback(h http.Handler, body, sig string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/callback/tripay", strings.NewReader(body))
	r.Header.Set("X-Callback-Signature", sig)
	r.Header.Set("X-Callback-Event", "payment_status")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://tripay.co.id") // cross-origin: only the CSRF bypass lets it through
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func (e *payEnv) pending(t *testing.T) db.PaymentRequest {
	t.Helper()
	rs, err := e.q.SearchPaymentRequests(t.Context(), db.SearchPaymentRequestsParams{PageLimit: 10})
	if err != nil || len(rs) != 1 {
		t.Fatalf("payment rows: %v %v", len(rs), err)
	}
	pr, _ := e.q.GetPaymentRequest(t.Context(), rs[0].ID)
	return pr
}

func (e *payEnv) trxCount(t *testing.T) int {
	l, _ := e.q.ListTransactions(t.Context(), db.ListTransactionsParams{Limit: 50})
	return len(l)
}

func TestPayOrderCreatesPendingAndRedirects(t *testing.T) {
	e := payApp(t)
	if w := do(e.h, "GET", "/portal/plans", nil, e.cookie); !strings.Contains(w.Body.String(), `value="QRIS"`) || strings.Contains(w.Body.String(), `value="OFF"`) {
		t.Fatalf("channels not listed: %d", w.Code)
	}
	w := e.order(t, url.Values{"channel": {"QRIS"}})
	if w.Code != 303 || w.Header().Get("Location") != "https://pay.example/T123" {
		t.Fatalf("order: %d %s", w.Code, w.Header().Get("Location"))
	}
	pr := e.pending(t)
	if pr.Status != "pending" || pr.GatewayRef != "T123" || pr.Amount != 100000 || pr.PayUrl == "" || pr.Channel != "QRIS" {
		t.Fatalf("row: %+v", pr)
	}
	if w := e.order(t, url.Values{"channel": {"NOPE"}}); w.Code != 200 {
		t.Fatalf("unknown channel: %d", w.Code)
	}
	if w := do(e.h, "GET", "/portal/payments/"+itoa(pr.ID), nil, e.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), "pending") {
		t.Fatalf("view: %d", w.Code)
	}
	// check status: gateway says PAID -> recharged once
	e.status = "PAID"
	for range 2 {
		do(e.h, "POST", "/portal/payments/"+itoa(pr.ID)+"/check", nil, e.cookie)
	}
	if got := e.pending(t); got.Status != "paid" || e.trxCount(t) != 1 {
		t.Fatalf("after check: %s trx=%d", got.Status, e.trxCount(t))
	}
}

func TestPayCouponDiscountsAmount(t *testing.T) {
	e := payApp(t)
	if _, err := e.q.CreateCoupon(t.Context(), db.CreateCouponParams{Code: "TEN", Type: "percent", Value: 10, StartDate: "2000-01-01", EndDate: "2999-01-01", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	e.order(t, url.Values{"channel": {"QRIS"}, "coupon": {"TEN"}})
	pr := e.pending(t)
	if pr.Amount != 90000 || pr.Coupon != "TEN" {
		t.Fatalf("row: %+v", pr)
	}
	body := fmt.Sprintf(`{"merchant_ref":%q,"status":"PAID"}`, pr.Ref)
	postCallback(e.h, body, signed(body))
	l, _ := e.q.ListTransactions(t.Context(), db.ListTransactionsParams{Limit: 5})
	if len(l) != 1 || l[0].Price != 90000 || l[0].Method != "Tripay - QRIS" {
		t.Fatalf("trx: %+v", l)
	}
}

func TestTripayCallbackPaidOnce(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	body := fmt.Sprintf(`{"merchant_ref":%q,"status":"PAID"}`, pr.Ref)

	// bad signature: rejected, nothing changes
	if w := postCallback(e.h, body, "deadbeef"); w.Code != 400 {
		t.Fatalf("bad sig: %d", w.Code)
	}
	if e.pending(t).Status != "pending" || e.trxCount(t) != 0 {
		t.Fatal("bad signature changed state")
	}

	// the same valid callback, sent concurrently and again afterwards
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := postCallback(e.h, body, signed(body)); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"success":true}` {
				t.Errorf("callback: %d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	postCallback(e.h, body, signed(body))
	got := e.pending(t)
	if got.Status != "paid" || !got.PaidAt.Valid || e.trxCount(t) != 1 {
		t.Fatalf("status=%s trx=%d", got.Status, e.trxCount(t))
	}
	subs, _ := e.q.ListSubscriptionsByCustomer(t.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: e.cust.ID, Limit: 10})
	if len(subs) != 1 {
		t.Fatalf("subs: %d", len(subs))
	}
}

func TestTripayCallbackFailedAndUnknown(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	body := fmt.Sprintf(`{"merchant_ref":%q,"status":"FAILED"}`, pr.Ref)
	postCallback(e.h, body, signed(body))
	if e.pending(t).Status != "failed" || e.trxCount(t) != 0 {
		t.Fatal("failed not marked")
	}
	unk := `{"merchant_ref":"NOPE","status":"PAID"}`
	if w := postCallback(e.h, unk, signed(unk)); w.Code != 200 {
		t.Fatalf("unknown ref: %d", w.Code)
	}
}

func TestExpirePayments(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	if err := e.s.Billing.ExpirePayments(t.Context()); err != nil || e.pending(t).Status != "pending" {
		t.Fatalf("expired too early: %v", err)
	}
	if _, err := e.s.conn.Exec("UPDATE payment_requests SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Minute).Unix(), pr.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Billing.ExpirePayments(t.Context()); err != nil || e.pending(t).Status != "expired" {
		t.Fatalf("not expired: %v", err)
	}
}

func TestPaymentSettings(t *testing.T) {
	e := payApp(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	if _, err := e.q.CreateAdmin(t.Context(), db.CreateAdminParams{Username: "adm", Fullname: "Adm", PasswordHash: string(hash), Role: "Admin"}); err != nil {
		t.Fatal(err)
	}
	adm := login(t, e.h, "adm")
	wantCode(t, do(e.h, "GET", "/admin/settings/payment", nil, adm), 403, "Admin gets payment settings")
	wantCode(t, do(e.h, "POST", "/admin/settings/payment", url.Values{"payment_gateway": {""}}, adm), 403, "Admin saves payment settings")

	w := do(e.h, "GET", "/admin/settings/payment", nil, e.c)
	if w.Code != 200 || strings.Contains(w.Body.String(), tripayKey) || strings.Contains(w.Body.String(), "M1x") ||
		!strings.Contains(w.Body.String(), "/callback/tripay") {
		t.Fatalf("settings page: %d (secret leaked or callback URL missing)", w.Code)
	}
	// empty secret keeps the stored one; a new one replaces it
	form := url.Values{"payment_gateway": {"tripay"}, "tripay_merchant_code": {"M2"}, "tripay_mode": {"sandbox"}}
	wantCode(t, do(e.h, "POST", "/admin/settings/payment", form, e.c), 303, "save")
	st, _ := e.s.loadSettings(t.Context())
	if st["tripay_private_key"] != tripayKey || st["tripay_merchant_code"] != "M2" {
		t.Fatalf("settings: %v", st)
	}
	form.Set("tripay_private_key", "new-key")
	do(e.h, "POST", "/admin/settings/payment", form, e.c)
	if st, _ = e.s.loadSettings(t.Context()); st["tripay_private_key"] != "new-key" {
		t.Fatal("secret not replaced")
	}
	form.Set("tripay_mode", "bogus")
	wantCode(t, do(e.h, "POST", "/admin/settings/payment", form, e.c), 422, "bad mode")
}

func TestPaymentAdminPages(t *testing.T) {
	e := payApp(t)
	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	if w := do(e.h, "GET", "/admin/payment-gateway", nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), "Tripay") {
		t.Fatalf("list: %d", w.Code)
	}
	if w := do(e.h, "GET", "/admin/payment-gateway/audit?status=pending&q=u1", nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), pr.Ref) {
		t.Fatalf("audit: %d", w.Code)
	}
	if w := do(e.h, "GET", "/admin/payment-gateway/audit?status=paid", nil, e.c); strings.Contains(w.Body.String(), pr.Ref) {
		t.Fatal("status filter ignored")
	}
	if w := do(e.h, "GET", "/admin/payment-gateway/audit/export", nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), pr.Ref) {
		t.Fatalf("csv: %d", w.Code)
	}
	if w := do(e.h, "GET", "/admin/payment-gateway/audit/"+itoa(pr.ID), nil, e.c); w.Code != 200 || !strings.Contains(w.Body.String(), "T123") {
		t.Fatalf("view: %d", w.Code)
	}
}

// A paid callback for a deleted customer is marked paid, and the operator is alerted once.
func TestTripayPaidForDeletedCustomerAlerts(t *testing.T) {
	e := payApp(t)
	got := make(chan string, 10)
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.URL.RawQuery + " " + string(b) // the telegram notifier puts the text in the query
	}))
	t.Cleanup(tg.Close)
	for k, v := range map[string]string{"telegram_bot": "T", "telegram_target_id": "1"} {
		if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := notify.Load(t.Context(), e.q)
	if err != nil {
		t.Fatal(err)
	}
	n.TelegramAPI = tg.URL
	e.s.Billing.Reload(n, time.UTC)

	e.order(t, url.Values{"channel": {"QRIS"}})
	pr := e.pending(t)
	if _, err := e.s.conn.Exec("UPDATE payment_requests SET customer_id = NULL WHERE id = ?", pr.ID); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"merchant_ref":%q,"status":"PAID"}`, pr.Ref)
	postCallback(e.h, body, signed(body))
	postCallback(e.h, body, signed(body))
	if e.pending(t).Status != "paid" || e.trxCount(t) != 0 {
		t.Fatalf("status=%s trx=%d", e.pending(t).Status, e.trxCount(t))
	}
	select {
	case msg := <-got:
		if !strings.Contains(msg, "dihapus") || !strings.Contains(msg, pr.Ref) {
			t.Fatalf("alert text: %s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no operator alert")
	}
	select {
	case msg := <-got:
		t.Fatalf("second callback alerted again: %s", msg)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestTripayPaidAfterClosed(t *testing.T) {
	for _, closed := range []string{"expired", "failed"} {
		e := payApp(t)
		e.order(t, url.Values{"channel": {"QRIS"}})
		pr := e.pending(t)
		if _, err := e.s.conn.Exec("UPDATE payment_requests SET status = ? WHERE id = ?", closed, pr.ID); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"merchant_ref":%q,"status":"PAID"}`, pr.Ref)
		postCallback(e.h, body, signed(body))
		postCallback(e.h, body, signed(body))
		if e.pending(t).Status != "paid" || e.trxCount(t) != 1 {
			t.Fatalf("%s: status=%s trx=%d", closed, e.pending(t).Status, e.trxCount(t))
		}
	}
}
