package web

// Setting role_access_mode. "strict" (default): deposit, Recharge Zero and voucher generation are
// for SuperAdmin/Admin, and the Report role reads reports only. "phpnuxbill": the old PHP rules,
// where Agent/Sales may do all of that and every role reads transactions and customer exports.

import (
	"context"
	"net/http"
)

// strictAccess reports whether role_access_mode is strict (anything but "phpnuxbill").
func (s *Server) strictAccess(ctx context.Context) bool { return s.ax.StrictAccess(ctx) }

// byMode picks the strict or the PHPNuxBill middleware per request, so a settings change applies at once.
func (s *Server) byMode(strict, php func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		a, b := strict(next), php(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.strictAccess(r.Context()) {
				a.ServeHTTP(w, r)
				return
			}
			b.ServeHTTP(w, r)
		})
	}
}
