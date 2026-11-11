package radius

import (
	"sync"
)

// failLimiter counts failed voucher-like attempts per key inside a sliding window.
// Keys sit in two generations: cur is replaced every voucherWindow, or early once it holds
// failMaxKeys, and the previous one is kept for lookups. Memory stays bounded and nothing is
// scanned per attempt; the old version scanned every key on each failure once it held 10000,
// which a spray of unknown usernames turned into most of the server's CPU.
// ponytail: an early rotation forgets the counts in old, so a spray of failMaxKeys usernames
// buys an attacker another voucherMaxFails tries on one account. In-memory, per process, empty MAC
// shares one bucket per NAS; use a DB table if several instances or restarts must share the count.
type failLimiter struct {
	mu       sync.Mutex
	cur, old map[string][]int64
	rotated  int64 // unix time cur was started
}

const (
	voucherMaxFails = 10
	voucherWindow   = 15 * 60
	failMaxKeys     = 50000
)

var voucherFails = &failLimiter{}

// recent returns the failures of key inside the window, oldest first. Callers hold mu.
func (l *failLimiter) recent(key string, now int64) []int64 {
	var t []int64
	for _, x := range append(l.old[key], l.cur[key]...) {
		if x > now-voucherWindow {
			t = append(t, x)
		}
	}
	return t
}

func (l *failLimiter) blocked(key string, now int64, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, now)) >= max
}

func (l *failLimiter) fail(key string, now int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := append(l.recent(key, now), now)
	if l.cur == nil || now-l.rotated >= voucherWindow || now < l.rotated || len(l.cur) >= failMaxKeys {
		l.old, l.cur, l.rotated = l.cur, map[string][]int64{}, now
	}
	delete(l.old, key)
	l.cur[key] = t
}

func (l *failLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.old, key)
	delete(l.cur, key)
}
