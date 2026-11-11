package web

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Parallel wrong passwords must not get more bcrypt checks than the lock allows: the attempt is
// counted before the check, not after it.
func TestLoginThrottleHoldsUnderParallelRequests(t *testing.T) {
	s, _ := newTestApp(t)
	h := s.Handler()
	bad := url.Values{"username": {"alice"}, "password": {"wrong"}}
	var checked atomic.Int32
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			if w := postFrom(h, "198.51.100.9:1", "/admin/login", bad); w.Code != http.StatusTooManyRequests {
				checked.Add(1)
			}
		})
	}
	wg.Wait()
	if n := checked.Load(); n > maxFailedLogins {
		t.Fatalf("%d parallel guesses checked, limit %d", n, maxFailedLogins)
	}
	// a correct password does not use up the lock
	good := url.Values{"username": {"alice"}, "password": {"secret123"}}
	for range maxFailedLogins + 2 {
		if w := postFrom(h, "203.0.113.5:1", "/admin/login", good); w.Code != http.StatusSeeOther {
			t.Fatalf("good login refused: %d", w.Code)
		}
	}
}

// Parallel wrong codes on one forgot-password session must not test more than otpTries codes. Once the
// flow is dropped, later requests are refused by the cleared session with the same message, so the
// count of messages cannot bound the checks; the right code sent after the storm must be refused.
func TestForgotOTPTriesHoldUnderParallelRequests(t *testing.T) {
	e, otp := forgotApp(t)
	ck := sessionOf(do(e.h, "POST", "/portal/forgot", url.Values{"username": {"u1"}}, nil))
	code := otp()
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	var dropped atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() {
			w := do(e.h, "POST", "/portal/forgot/verify", url.Values{"otp_code": {wrong}}, ck)
			if strings.Contains(w.Body.String(), tr("Too many invalid attempts, please request a new Verification Code")) {
				dropped.Add(1)
			}
		})
	}
	wg.Wait()
	if dropped.Load() == 0 {
		t.Fatal("the flow was never dropped after parallel wrong codes")
	}
	w := do(e.h, "POST", "/portal/forgot/verify", url.Values{"otp_code": {code}}, ck)
	if strings.Contains(w.Body.String(), tr("Verification Code Valid")) {
		t.Fatal("the right code was accepted after the tries ran out")
	}
}

func TestRestOctetsClamp(t *testing.T) {
	for _, c := range []struct{ g, o, want int64 }{
		{0, 100, 100},
		{1, 5, 1<<32 | 5},
		{-1, -5, 0},
		{1 << 40, 0, (1<<31 - 1) << 32},
		{0, 1 << 40, 1<<32 - 1},
	} {
		if got := restOctets(c.g, c.o); got != c.want || got < 0 {
			t.Errorf("restOctets(%d, %d) = %d, want %d", c.g, c.o, got, c.want)
		}
	}
}
