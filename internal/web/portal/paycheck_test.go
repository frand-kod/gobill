package portal

import (
	"testing"
	"time"
)

// The payment check waits 30 seconds per payment; the message gets the seconds left.
func TestPayCheckWaitSeconds(t *testing.T) {
	s := &Handlers{}
	if w := s.PayCheckWait(1); w != 0 {
		t.Fatalf("first check held back for %d s", w)
	}
	if w := s.PayCheckWait(1); w < 25 || w > 30 {
		t.Fatalf("second check waits %d s, want the seconds left of the 30 s gap", w)
	}
	if w := s.PayCheckWait(2); w != 0 {
		t.Fatalf("another payment held back for %d s", w)
	}
	s.PayCheckMu.Lock()
	s.PayCheckAt[1] = time.Now().Add(-PayCheckGap)
	s.PayCheckMu.Unlock()
	if w := s.PayCheckWait(1); w != 0 {
		t.Fatalf("check after the gap held back for %d s", w)
	}
}
