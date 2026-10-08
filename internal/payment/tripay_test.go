package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newT(url string) *Tripay {
	t, _ := NewTripay(map[string]string{"api_key": "AK", "private_key": "PK", "merchant_code": "T1", "channel": "BRIVA"})
	t.BaseURL = url
	return t
}

func TestValidateConfig(t *testing.T) {
	if _, err := NewTripay(map[string]string{"api_key": "a", "private_key": "b"}); err == nil {
		t.Fatal("missing merchant_code accepted")
	}
	if _, err := NewTripay(map[string]string{"api_key": "a", "private_key": "b", "merchant_code": "c", "mode": "x"}); err == nil {
		t.Fatal("bad mode accepted")
	}
	g, _ := NewTripay(map[string]string{"api_key": "a", "private_key": "b", "merchant_code": "c"})
	if !strings.Contains(g.BaseURL, "api-sandbox") {
		t.Fatal(g.BaseURL)
	}
}

func TestCreate(t *testing.T) {
	var body map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/transaction/create" || r.Method != "POST" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"success":true,"data":{"reference":"R1","checkout_url":"https://pay/x"}}`))
	}))
	defer srv.Close()
	g := newT(srv.URL)
	c, err := g.CreateTransaction(context.Background(), Transaction{ID: "INV1", Amount: 50000, PlanName: "P", Name: "N"})
	if err != nil || c.PayURL != "https://pay/x" || c.Reference != "R1" {
		t.Fatal(c, err)
	}
	if auth != "Bearer AK" || body["method"] != "BRIVA" || body["amount"] != float64(50000) {
		t.Fatal(auth, body)
	}
	if body["signature"] != g.sign("T1INV150000") {
		t.Fatal("signature", body["signature"])
	}
}

func TestStatusAndChannels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/transaction/detail":
			if r.URL.Query().Get("reference") != "R1" {
				t.Error("reference")
			}
			w.Write([]byte(`{"success":true,"data":{"status":"PAID"}}`))
		case "/merchant/payment-channel":
			w.Write([]byte(`{"success":true,"data":[{"code":"BRIVA","name":"BRI VA","group":"Virtual Account","active":true}]}`))
		}
	}))
	defer srv.Close()
	g := newT(srv.URL)
	if st, err := g.Status(context.Background(), Transaction{Reference: "R1"}); err != nil || st != Paid {
		t.Fatal(st, err)
	}
	if ch, err := g.Channels(context.Background()); err != nil || len(ch) != 1 || ch[0].Code != "BRIVA" {
		t.Fatal(ch, err)
	}
	for in, want := range map[string]Status{"UNPAID": Pending, "FAILED": Failed, "REFUND": Failed, "EXPIRED": Expired} {
		if got, _ := mapStatus(in); got != want {
			t.Errorf("%s -> %s", in, got)
		}
	}
	if _, err := mapStatus("???"); err == nil {
		t.Error("unknown status accepted")
	}
}

func TestCallback(t *testing.T) {
	g := newT("")
	body := `{"reference":"R1","merchant_ref":"INV1","status":"PAID"}`
	req := func(b, sig, ev string) *http.Request {
		r := httptest.NewRequest("POST", "/cb", strings.NewReader(b))
		r.Header.Set("X-Callback-Signature", sig)
		r.Header.Set("X-Callback-Event", ev)
		return r
	}
	id, st, err := g.HandleCallback(req(body, g.sign(body), "payment_status"))
	if err != nil || id != "INV1" || st != Paid {
		t.Fatal(id, st, err)
	}
	if _, _, err := g.HandleCallback(req(strings.Replace(body, "PAID", "FAILED", 1), g.sign(body), "payment_status")); err == nil {
		t.Error("tampered body accepted")
	}
	if _, _, err := g.HandleCallback(req(body, "deadbeef", "payment_status")); err == nil {
		t.Error("bad sig accepted")
	}
	if _, _, err := g.HandleCallback(req(body, "", "payment_status")); err == nil {
		t.Error("empty sig accepted")
	}
	if _, _, err := g.HandleCallback(req(body, g.sign(body), "other")); err == nil {
		t.Error("wrong event accepted")
	}
}
