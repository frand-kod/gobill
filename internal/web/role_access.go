package web

// Setting role_access_mode. "strict" (default): deposit, Recharge Zero and voucher generation are
// for SuperAdmin/Admin, and the Report role reads reports only. "phpnuxbill": the old PHP rules,
// where Agent/Sales may do all of that and every role reads transactions and customer exports.

import (
	"context"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/httpx"

	"github.com/frand-kod/gobill/internal/db"
)

func init() { settingsSuperField["role_access_mode"] = true }

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

// ctxIsManager: the admin on ctx (set by requireAdmin) is SuperAdmin or Admin.
func ctxIsManager(ctx context.Context) bool { return adminx.CtxIsManager(ctx) }

// canOpenInvoices matches the invoice route (noReport): strict mode keeps the Report role out.
func (s *Server) canOpenInvoices(ctx context.Context) bool {
	a, _ := ctx.Value(ctxKey{}).(*db.Admin)
	return a != nil && (!s.strictAccess(ctx) || httpx.OneOf(a.Role, "SuperAdmin", "Admin", "Agent", "Sales"))
}
