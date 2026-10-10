package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// notify_customers=no stops customer sends; a missing key still sends.
func TestCustomersSwitch(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	st := map[string]string{"sms_url": srv.URL + "/sms?n=[number]&t=[text]", "user_notification_expired": "sms", "user_notification_payment": "sms"}
	n, c, ctx := nt(st), db.Customer{Phone: "0812345678"}, context.Background()
	if err := n.Expired(ctx, c, "Gold", nil); err != nil || hits.Load() != 1 {
		t.Fatalf("default: hits %d err %v", hits.Load(), err)
	}
	st["notify_customers"] = "no"
	if err := n.Expired(ctx, c, "Gold", nil); err != nil || hits.Load() != 1 {
		t.Fatalf("off expired: hits %d err %v", hits.Load(), err)
	}
	if err := n.RechargeSuccess(ctx, c, map[string]string{"invoice": "INV-1"}); err != nil || hits.Load() != 1 {
		t.Fatalf("off recharge: hits %d err %v", hits.Load(), err)
	}
}

func nt(s map[string]string) *Notifier {
	return &Notifier{Settings: s, HTTP: http.DefaultClient, TelegramAPI: "https://api.telegram.org"}
}

func TestRender(t *testing.T) {
	got := Render("Hi [[name]] [[x]] [[name]]", map[string]string{"name": "Ann"})
	if got != "Hi Ann [[x]] Ann" {
		t.Fatal(got)
	}
}

func TestTelegram(t *testing.T) {
	var path, q string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path, q = r.URL.Path, r.URL.RawQuery }))
	defer srv.Close()
	n := nt(map[string]string{"telegram_bot": "123:abc", "telegram_target_id": "-42"})
	n.TelegramAPI = srv.URL
	if err := n.Telegram(context.Background(), "a b&c"); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:abc/sendMessage" || q != "chat_id=-42&text=a+b%26c" {
		t.Fatal(path, q)
	}
}

func TestGatewayAndChannel(t *testing.T) {
	var sms, wa string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sms" {
			sms = r.URL.RawQuery
		} else {
			wa = r.URL.RawQuery
		}
	}))
	defer srv.Close()
	n := nt(map[string]string{
		"sms_url": srv.URL + "/sms?to=[number]&m=[text]", "wa_url": srv.URL + "/wa?to=[number]&m=[text]",
		"country_code_phone": "62", "user_notification_expired": "wa",
	})
	ctx := context.Background()
	if err := n.SMS(ctx, "+62 81", "hi & bye"); err != nil {
		t.Fatal(err)
	}
	if sms != "to=%2B62+81&m=hi+%26+bye" {
		t.Fatal(sms)
	}
	c := db.Customer{Fullname: "Ann", Phone: "0812345"}
	if err := n.Expired(ctx, c, "Gold", nil); err != nil {
		t.Fatal(err)
	}
	if wa != "to=62812345&m=Hello+Ann%2C+your+internet+package+Gold+has+been+expired." {
		t.Fatal(wa)
	}
}

func TestWebhookSignature(t *testing.T) {
	var body []byte
	var sig, ev string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		sig, ev = r.Header.Get("X-Signature"), r.Header.Get("X-Event")
	}))
	defer srv.Close()
	n := nt(map[string]string{"webhook_url": srv.URL, "webhook_secret": "s3"})
	if err := n.Webhook(context.Background(), "payment.paid", map[string]int{"id": 1}); err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha256.New, []byte("s3"))
	m.Write(body)
	if ev != "payment.paid" || sig != "sha256="+hex.EncodeToString(m.Sum(nil)) {
		t.Fatal(ev, sig)
	}
}

type failRT struct{ t *testing.T }

func (f failRT) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("unexpected request")
	return nil, nil
}

func TestEmptyConfigSkips(t *testing.T) {
	n := nt(map[string]string{"user_notification_expired": "wa", "user_notification_payment": "email"})
	n.HTTP = &http.Client{Transport: failRT{t}}
	ctx := context.Background()
	c := db.Customer{Phone: "0812345", Email: "a@b.c"}
	for _, err := range []error{n.Telegram(ctx, "x"), n.SMS(ctx, "1", "x"), n.WhatsApp(ctx, "1", "x"),
		n.Email(ctx, "a@b.c", "s", "b"), n.Webhook(ctx, "e", 1), n.Expired(ctx, c, "p", nil), n.RechargeSuccess(ctx, c, nil)} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmailConfigValidation(t *testing.T) {
	if _, _, err := nt(map[string]string{"smtp_host": "h", "mail_from": "a@b.c"}).mailer("x@y.z", "s", "b"); err == nil {
		t.Fatal("want port error")
	}
	if _, _, err := nt(map[string]string{"smtp_host": "h", "smtp_port": "25"}).mailer("x@y.z", "s", "b"); err == nil {
		t.Fatal("want mail_from error")
	}
	if _, _, err := nt(map[string]string{"smtp_host": "h", "smtp_port": "465", "smtp_ssltls": "ssl", "mail_from": "a@b.c"}).mailer("x@y.z", "s", "b"); err != nil {
		t.Fatal(err)
	}
}
