package web

import "time"

// otpAllow records and permits one OTP send: per IP max 5 per 15 min; per phone a 60s cooldown
// and max 5 per hour. An empty phone only counts against the IP.
// ponytail: in-memory, resets on restart; persist if abuse needs to survive restarts.
func (s *Server) otpAllow(ip, phone string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.otpSent == nil {
		s.otpSent = map[string][]time.Time{}
	}
	now := time.Now()
	recent := func(k string, win time.Duration) []time.Time {
		var l []time.Time
		for _, t := range s.otpSent[k] {
			if now.Sub(t) < win {
				l = append(l, t)
			}
		}
		return l
	}
	ik, pk := "ip:"+ip, "ph:"+phone
	il, pl := recent(ik, 15*time.Minute), recent(pk, time.Hour)
	if len(il) >= 5 || phone != "" && (len(pl) >= 5 || len(pl) > 0 && now.Sub(pl[len(pl)-1]) < time.Minute) {
		return false
	}
	s.otpSent[ik] = append(il, now)
	if phone != "" {
		s.otpSent[pk] = append(pl, now)
	}
	return true
}
