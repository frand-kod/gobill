package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newD(base string) *Duitku {
	d, _ := NewDuitku(map[string]string{"api_key": "AK", "merchant_code": "D1", "channel": "VC"})
	d.BaseURL = base
	return d
}

func TestDuitkuConfig(t *testing.T) {
	if _, err := NewDuitku(map[string]string{"api_key": "a"}); err == nil {
		t.Fatal("missing merchant_code accepted")
	}
	if _, err := NewDuitku(map[string]string{"api_key": "a", "merchant_code": "c", "mode": "x"}); err == nil {
		t.Fatal("bad mode accepted")
	}
	g, _ := NewDuitku(map[string]string{"api_key": "a", "merchant_code": "c"})
	if !strings.Contains(g.BaseURL, "sandbox.duitku.com") {
		t.Fatal(g.BaseURL)
	}
	g, _ = NewDuitku(map[string]string{"api_key": "a", "merchant_code": "c", "mode": "production"})
	if !strings.Contains(g.BaseURL, "passport.duitku.com") {
		t.Fatal(g.BaseURL)
	}
}

func TestDuitkuCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/inquiry" || r.Method != "POST" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"reference":"R1","paymentUrl":"https://pay/x","statusCode":"00","statusMessage":"SUCCESS"}`))
	}))
	defer srv.Close()
	g := newD(srv.URL)
	c, err := g.CreateTransaction(context.Background(), Transaction{ID: "INV1", Amount: 50000, PlanName: "P",
		Name: "A very long customer name indeed", ReturnURL: "https://isp.example/portal/payments/1", CallbackURL: "https://isp.example/callback/duitku", Expiry: time.Now().Add(time.Hour)})
	if err != nil || c.PayURL != "https://pay/x" || c.Reference != "R1" {
		t.Fatal(c, err)
	}
	if body["paymentMethod"] != "VC" || body["paymentAmount"] != float64(50000) || body["callbackUrl"] != "https://isp.example/callback/duitku" {
		t.Fatal(body)
	}
	if body["signature"] != g.sign("D1INV150000") {
		t.Fatal("signature", body["signature"])
	}
	if n := []rune(body["customerVaName"].(string)); len(n) != 20 {
		t.Fatal("va name not truncated", len(n))
	}
}

func TestDuitkuCreateErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"Message":"Wrong signature"}`))
	}))
	defer srv.Close()
	_, err := newD(srv.URL).CreateTransaction(context.Background(), Transaction{ID: "I", Amount: 1, ReturnURL: "https://h/x", CallbackURL: "https://h/callback/duitku"})
	if err == nil || !strings.Contains(err.Error(), "Wrong signature") {
		t.Fatal(err)
	}
	if _, err := newD(srv.URL).CreateTransaction(context.Background(), Transaction{ID: "I", Amount: 1}); err == nil {
		t.Fatal("missing callback url accepted")
	}
}

func TestDuitkuStatusAndChannels(t *testing.T) {
	code := "00"
	g := newD("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		switch r.URL.Path {
		case "/transactionStatus":
			if b["merchantOrderId"] != "INV1" || b["signature"] != g.sign("D1INV1") {
				t.Error("status request", b)
			}
			w.Write([]byte(`{"statusCode":"` + code + `"}`))
		case "/paymentmethod/getpaymentmethod":
			if b["signature"] != g.sign("D110000"+b["datetime"].(string)) {
				t.Error("channels signature", b)
			}
			w.Write([]byte(`{"paymentFee":[{"paymentMethod":"VC","paymentName":"Credit Card","totalFee":"0"}],"responseCode":"00","responseMessage":"SUCCESS"}`))
		}
	}))
	defer srv.Close()
	g.BaseURL = srv.URL
	for c, want := range map[string]Status{"00": Paid, "01": Pending, "02": Failed} {
		code = c
		if st, err := g.Status(context.Background(), Transaction{ID: "INV1"}); err != nil || st != want {
			t.Errorf("%s -> %s %v", c, st, err)
		}
	}
	code = "99"
	if _, err := g.Status(context.Background(), Transaction{ID: "INV1"}); err == nil {
		t.Error("unknown status accepted")
	}
	if ch, err := g.Channels(context.Background()); err != nil || len(ch) != 1 || ch[0].Code != "VC" || !ch[0].Active {
		t.Fatal(ch, err)
	}
}

func TestDuitkuCallback(t *testing.T) {
	g := newD("")
	req := func(code, amt, id, rc, sig string) *http.Request {
		f := url.Values{"merchantCode": {code}, "amount": {amt}, "merchantOrderId": {id}, "resultCode": {rc}, "signature": {sig}}
		r := httptest.NewRequest("POST", "/cb", strings.NewReader(f.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	sig := g.sign("D150000INV1")
	if id, st, amt, err := g.HandleCallback(req("D1", "50000", "INV1", "00", sig)); err != nil || id != "INV1" || st != Paid || amt != 50000 {
		t.Fatal(id, st, amt, err)
	}
	if id, st, _, err := g.HandleCallback(req("D1", "50000", "INV1", "01", sig)); err != nil || id != "INV1" || st != Failed {
		t.Fatal(id, st, err)
	}
	if _, _, _, err := g.HandleCallback(req("D1", "1", "INV1", "00", sig)); err == nil {
		t.Error("tampered amount accepted")
	}
	if _, _, _, err := g.HandleCallback(req("D1", "50000", "INV1", "00", "deadbeef")); err == nil {
		t.Error("bad sig accepted")
	}
	if _, _, _, err := g.HandleCallback(req("D1", "50000", "INV1", "00", "")); err == nil {
		t.Error("empty sig accepted")
	}
	if _, _, _, err := g.HandleCallback(req("D1", "50000", "INV1", "07", sig)); err == nil {
		t.Error("unknown resultCode accepted")
	}
	if _, _, _, err := g.HandleCallback(req("D1", "abc", "INV1", "00", g.sign("D1abcINV1"))); err == nil {
		t.Error("non-numeric amount accepted")
	}
	other := newD("")
	other.MerchantCode = "D2"
	if _, _, _, err := g.HandleCallback(req("D2", "50000", "INV1", "00", other.sign("D250000INV1"))); err == nil {
		t.Error("foreign merchant code accepted")
	}
}
