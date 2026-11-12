package web

// Route registration: the main mux table and per-area route groups.

import (
	"io/fs"
	"net/http"

	"strings"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/admin/billing"
	"github.com/frand-kod/gobill/internal/web/admin/customers"
	"github.com/frand-kod/gobill/internal/web/admin/network"
	"github.com/frand-kod/gobill/internal/web/admin/plans"
	"github.com/frand-kod/gobill/internal/web/admin/reports"
	adminsettings "github.com/frand-kod/gobill/internal/web/admin/settings"
	"github.com/frand-kod/gobill/internal/web/portal"
	"github.com/frand-kod/gobill/internal/web/public"
)

// prefetchSafe is the session middleware with one change: a prefetch (Sec-Purpose, or Purpose on older
// builds) reads the session but never saves it, so a flash or a one-time value (metrics_token_once) survives
// for the real navigation. Responses vary on Sec-Purpose so a cached prefetch is never served as a navigation.
// ponytail: a prefetched page can be a few minutes old when shown; acceptable for intent-based prefetch.
func (s *Server) prefetchSafe(next http.Handler) http.Handler {
	saved := s.Sessions.LoadAndSave(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Sec-Purpose")
		if !isPrefetch(r) {
			saved.ServeHTTP(w, r)
			return
		}
		token := ""
		if c, err := r.Cookie(s.Sessions.Cookie.Name); err == nil {
			token = c.Value
		}
		ctx, err := s.Sessions.Load(r.Context(), token)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isPrefetch(r *http.Request) bool {
	for _, h := range []string{r.Header.Get("Sec-Purpose"), r.Header.Get("Purpose")} {
		h = strings.ToLower(h)
		if strings.Contains(h, "prefetch") || strings.Contains(h, "prerender") {
			return true
		}
	}
	return false
}

// Customer actions, login-as, welcome message, portal activation list / buy for friend /
// forgot username, and log cleanup.

func (s *Server) extraRoutes(mux *http.ServeMux, managers func(http.Handler) http.Handler) {
	mux.Handle("POST /admin/customers/{id}/login", managers(http.HandlerFunc(s.custLoginAs)))

	mux.HandleFunc("POST /portal/impersonate/end", s.pImpersonateEnd)
}

// messageRoutes registers the admin side; staff is the SuperAdmin/Admin/Agent/Sales middleware.
func (s *Server) messageRoutes(mux *http.ServeMux, staff func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/message/send", staff(http.HandlerFunc(s.msgSendForm)))
	mux.Handle("POST /admin/message/send", staff(http.HandlerFunc(s.msgSend)))
	mux.Handle("GET /admin/message/bulk", staff(http.HandlerFunc(s.msgBulkForm)))
	mux.Handle("POST /admin/message/bulk", staff(http.HandlerFunc(s.msgBulkStart)))
	mux.Handle("POST /admin/message/selected", staff(http.HandlerFunc(s.msgSelectedSend)))
	mux.Handle("GET /admin/message/bulk/status", staff(http.HandlerFunc(s.msgBulkStatus)))
}

func (s *Server) portalRoutes(mux *http.ServeMux) {
	portal.Register(mux, s.portal)
}

// docsRoutes serves the "Panduan" guides to any logged-in admin.
func (s *Server) docsRoutes(mux *http.ServeMux, all func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/docs", all(http.HandlerFunc(s.docsIndex)))
	mux.Handle("GET /admin/docs/{slug}", all(http.HandlerFunc(s.docsPage)))
}

// Handler returns the router wrapped in CSRF protection and session loading.
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(gobill.FS, "web/static")
	if err != nil {
		panic(err) // the path is embedded at build time
	}
	mux := http.NewServeMux()
	all := s.requireAdmin()
	managers := s.requireAdmin("SuperAdmin", "Admin")
	// Old PHP: bandwidth, routers, pool and logs are SuperAdmin/Admin only; customers are
	// readable by everyone, creatable by Agent/Sales too, editable/deletable by managers.
	staff := s.requireAdmin("SuperAdmin", "Admin", "Agent", "Sales")
	// role_access_mode: strict narrows these to managers (money) or staff (reading customer data);
	// phpnuxbill keeps the old PHP rules.
	money := s.byMode(managers, staff)
	noReport := s.byMode(staff, all)
	mw := adminx.Mw{All: all, Managers: managers, Staff: staff, NoReport: noReport, Money: money, SuperAdmin: s.requireAdmin("SuperAdmin"), Require: s.requireAdmin}
	network.Register(mux, s.net, mw)
	customers.Register(mux, s.customers, mw)
	plans.Register(mux, s.plans, mw)
	billing.Register(mux, s.billing, mw)
	reports.Register(mux, s.reports, mw)
	adminsettings.Register(mux, s.settings, mw)

	// auth: root redirect, static files, uploads, admin login/logout, own password
	mux.HandleFunc("GET /{$}", s.root)
	mux.Handle("GET /static/", staticHandler(static))
	mux.HandleFunc("GET /admin/login", s.loginForm)
	mux.HandleFunc("GET /uploads/{name}", s.serveUpload)
	mux.HandleFunc("POST /admin/login", s.loginSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /admin/login/2fa", s.login2FAForm)
	mux.HandleFunc("POST /admin/login/2fa", s.login2FASubmit)

	// app manifests (public: the browser fetches them without cookies)
	mux.HandleFunc("GET /admin/manifest.webmanifest", s.manifest(" Admin", "/admin", "/admin/"))
	mux.HandleFunc("GET /portal/manifest.webmanifest", s.manifest("", "/portal", "/portal/"))

	// dashboard

	// customers: search, list, detail, edit, delete, actions, custom fields, maps
	mux.Handle("POST /admin/customers/message", staff(http.HandlerFunc(s.custMessageForm)))
	s.extraRoutes(mux, managers)
	// Old customfield.php: custom fields CRUD (managers only).
	// F5 maps and ODP (maps.go, odp.go). Old PHP: customer map is open to all admins; router, ODP and their maps are managers only.

	// recharge and billing: recharge, deposit, customer plans, transactions, payment gateway admin
	mux.Handle("GET /admin/subscriptions", all(http.HandlerFunc(s.subList)))
	mux.Handle("GET /admin/subscriptions/export", noReport(http.HandlerFunc(s.subExport)))
	mux.Handle("GET /admin/isolations", all(http.HandlerFunc(s.isoList)))
	mux.Handle("GET /admin/isolations/export", noReport(http.HandlerFunc(s.isoExport)))
	mux.Handle("GET /admin/subscriptions/{id}/edit", managers(http.HandlerFunc(s.subEdit)))
	mux.Handle("POST /admin/subscriptions/{id}", managers(http.HandlerFunc(s.subSave)))
	mux.Handle("POST /admin/subscriptions/{id}/extend", staff(http.HandlerFunc(s.subExtend)))
	mux.Handle("POST /admin/subscriptions/{id}/deactivate", managers(http.HandlerFunc(s.subDeactivate)))
	mux.Handle("POST /admin/subscriptions/{id}/sync", managers(http.HandlerFunc(s.subSync)))

	// vouchers and coupons
	// Old PHP plan.php: voucher list is open to all admins, generate/redeem to staff, delete to managers.

	// agent consignment batches: managers write, an Agent reads its own
	// Old PHP coupons.php: every action is SuperAdmin/Admin/Sales only.

	// plans and network: bandwidth, plans, pools, routers, NAS, online sessions, ODP and router/ODP maps
	mux.Handle("GET /admin/routers/{id}/reconcile", managers(http.HandlerFunc(s.routerReconcile)))
	mux.Handle("POST /admin/routers/{id}/reconcile", managers(http.HandlerFunc(s.routerReconcile)))
	mux.Handle("POST /admin/radius/sessions/disconnect-many", managers(http.HandlerFunc(s.radiusDisconnectMany)))
	mux.Handle("POST /admin/radius/sessions/{id}/disconnect", managers(http.HandlerFunc(s.radiusDisconnect)))
	mux.Handle("GET /admin/radius/ip-lookup", managers(http.HandlerFunc(s.ipLookupForm)))
	mux.Handle("POST /admin/radius/ip-lookup", managers(http.HandlerFunc(s.ipLookupRun)))
	mux.Handle("GET /admin/radius/test-login", managers(http.HandlerFunc(s.testLoginForm)))
	mux.Handle("POST /admin/radius/test-login", managers(http.HandlerFunc(s.testLoginRun)))

	// reports and logs
	s.docsRoutes(mux, all)

	superAdmin := s.requireAdmin("SuperAdmin")
	s.setupRoutes(mux, superAdmin)
	mux.Handle("GET /admin/settings/isolir/preview", superAdmin(http.HandlerFunc(s.public.IsolirPreview)))

	// messages: send, bulk, selected customers
	s.messageRoutes(mux, staff)

	// customer portal
	s.portalRoutes(mux)
	public.Register(mux, s.public)

	// the Tripay server posts the callback without our origin; its HMAC signature authenticates it
	cop := http.NewCrossOriginProtection()
	for _, p := range public.OriginBypass {
		cop.AddInsecureBypassPattern(p)
	}
	// isolirGate sits inside realIP (it needs the real client IP) and outside the session chain
	app := s.realIP(s.public.IsolirGate(cop.Handler(s.prefetchSafe(s.idleGuard(s.maintenance(s.styledNotFound(mux)))))))
	// /health sits outside the session and maintenance chain so monitors always get an answer
	top := http.NewServeMux()
	top.HandleFunc("GET /health", s.health)
	top.HandleFunc("GET /metrics", s.reports.MetricsHandler)
	top.HandleFunc("GET /licenses", licenses)
	top.Handle("/", app)
	return cacheDefaults(top)
}
