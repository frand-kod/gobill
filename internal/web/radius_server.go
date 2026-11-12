package web

import "github.com/frand-kod/gobill/internal/radius"

// radiusServer returns the shared instance (so throttles persist across requests);
// tests without one get a single lazily built instance.
func (s *Server) radiusServer() *radius.Server {
	s.radiusOnce.Do(func() {
		if s.Radius != nil {
			return
		}
		s.Radius = &radius.Server{Q: s.Queries, Key: s.SecretKey, Trusted: func() bool { return s.ClockWarning == nil || s.ClockWarning() == "" }}
		if s.Billing != nil {
			s.Radius.Redeem = s.Billing.RedeemVoucher
			s.Radius.Start = s.Billing.StartPending
		}
	})
	return s.Radius
}
