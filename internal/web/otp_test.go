package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/notify"
)

// phone_otp_type "both": a WhatsApp failure still tries SMS, and the send fails only when both do.
func TestDeliverOTPBothFallsBack(t *testing.T) {
	var waHits, smsHits atomic.Int32
	var smsStatus atomic.Int32
	smsStatus.Store(http.StatusOK)
	wa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		waHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer wa.Close()
	sms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		smsHits.Add(1)
		w.WriteHeader(int(smsStatus.Load()))
	}))
	defer sms.Close()
	n := &notify.Notifier{Settings: map[string]string{"wa_url": wa.URL + "/?to=[number]&text=[text]", "sms_url": sms.URL + "/?to=[number]&text=[text]"},
		HTTP: http.DefaultClient, Timeout: 5 * time.Second}

	if err := deliverOTP(context.Background(), n, "both", "0812345678", "123456"); err != nil {
		t.Fatalf("WhatsApp down, SMS up: got %v, want nil", err)
	}
	if waHits.Load() != 1 || smsHits.Load() != 1 {
		t.Fatalf("hits wa=%d sms=%d, want 1 and 1", waHits.Load(), smsHits.Load())
	}

	smsStatus.Store(http.StatusInternalServerError)
	err := deliverOTP(context.Background(), n, "both", "0812345678", "123456")
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 {
		t.Fatalf("both down: got %v, want both failures joined", err)
	}
}
