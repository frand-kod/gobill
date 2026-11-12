package core

// In-memory login and attempt limiter.

import (
	"strings"
	"time"
)

const (
	MaxFailedLogins = 10
	MaxFailedPerIP  = 100 // loose per-IP cap across usernames
	LoginWindow     = 15 * time.Minute
)

// Per-username delay (see UserDelay); vars so tests can zero them.
var (
	LoginDelayStep = 250 * time.Millisecond
	LoginDelayMax  = 3 * time.Second
)

func (s *Core) TooManyFailures(ip string) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return len(s.Recent(ip)) >= MaxFailedLogins
}

// Lock key is ip|username so customers sharing one CGNAT IP don't lock each other out; a loose
// per-IP cap ("~"+ip, never cleared by a success) still stops one IP spraying many usernames.
func LoginKey(ip, user string) string { return ip + "|" + strings.ToLower(strings.TrimSpace(user)) }

// Reserve counts one attempt against key before the check runs and returns how many are left after it,
// or -1 when key is already at limit. Check and count happen under one lock, so parallel requests
// cannot all pass a check that a later RecordFailure would have failed. A success that must not count
// calls Unreserve.
func (s *Core) Reserve(key string, limit int) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	n := len(s.Recent(key))
	if n >= limit {
		return -1
	}
	s.Failed[key] = append(s.Failed[key], time.Now())
	return limit - n - 1
}

// Unreserve takes back the newest attempt Reserve counted.
func (s *Core) Unreserve(key string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if l := s.Recent(key); len(l) > 0 {
		s.Failed[key] = l[:len(l)-1]
	}
}

// LoginReserve counts one login attempt against ip|user and the loose per-IP cap, and says false when
// either is over its limit. A correct password calls LoginForgive, a wrong one leaves both counted.
func (s *Core) LoginReserve(ip, user string) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	k, pk := LoginKey(ip, user), "~"+ip
	if len(s.Recent(k)) >= MaxFailedLogins || len(s.Recent(pk)) >= MaxFailedPerIP {
		return false
	}
	now := time.Now()
	s.Failed[k] = append(s.Failed[k], now)
	s.Failed[pk] = append(s.Failed[pk], now)
	return true
}

// LoginForgive takes back the attempt LoginReserve counted, for a correct password.
func (s *Core) LoginForgive(ip, user string) {
	s.Unreserve(LoginKey(ip, user))
	s.Unreserve("~" + ip)
}

// UserDelay slows guessing against one username, whatever the client IP, by LoginDelayStep per recent
// failure up to LoginDelayMax. It never locks: a hard per-username lock would let anyone lock admin out.
// ponytail: the delay applies to 2FA accounts too; exempt them if the wait annoys real admins.
func (s *Core) UserDelay(uk string) {
	s.Mu.Lock()
	n := len(s.Recent(uk))
	s.Mu.Unlock()
	time.Sleep(min(time.Duration(n)*LoginDelayStep, LoginDelayMax))
}

func (s *Core) RecordFailure(ip string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if len(s.Failed) > 10000 { // bound memory: recent() drops keys whose failures have expired
		for k := range s.Failed {
			s.Recent(k)
		}
	}
	s.Failed[ip] = append(s.Recent(ip), time.Now())
}

// LockedCount returns how many login keys (client IPs; usernames only delay, see UserDelay) are over the failure limit now.
func (s *Core) LockedCount() int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	n := 0
	for k := range s.Failed {
		if !strings.HasPrefix(k, "u:") && !strings.HasPrefix(k, "~") && !strings.HasPrefix(k, "cu:") && !strings.HasPrefix(k, "otp:") && len(s.Recent(k)) >= MaxFailedLogins {
			n++
		}
	}
	return n
}

func (s *Core) ClearFailures(ip string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	delete(s.Failed, ip)
}

// Recent drops failures older than the window. Caller holds s.Mu.
func (s *Core) Recent(ip string) []time.Time {
	cutoff := time.Now().Add(-LoginWindow)
	list := s.Failed[ip][:0]
	for _, t := range s.Failed[ip] {
		if t.After(cutoff) {
			list = append(list, t)
		}
	}
	if len(list) == 0 {
		delete(s.Failed, ip)
	} else {
		s.Failed[ip] = list
	}
	return list
}
