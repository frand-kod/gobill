package web

// Route registration: the main mux table and per-area route groups.

import (
	"io/fs"
	"net/http"

	"strings"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/admin/customers"
	"github.com/frand-kod/gobill/internal/web/admin/network"
	"github.com/frand-kod/gobill/internal/web/admin/plans"
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
	mux.Handle("POST /admin/logs/clean/{kind}", managers(http.HandlerFunc(s.logClean)))

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

// ---- admin (A71-A73) ----

func (s *Server) paymentAdminRoutes(mux *http.ServeMux, managers func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/payment-gateway", managers(http.HandlerFunc(s.pgList)))
	mux.Handle("GET /admin/payment-gateway/audit", managers(http.HandlerFunc(s.pgAudit)))
	mux.Handle("GET /admin/payment-gateway/audit/export", managers(http.HandlerFunc(s.pgAuditExport)))
	mux.Handle("GET /admin/payment-gateway/audit/{id}", managers(http.HandlerFunc(s.pgAuditView)))
}

func (s *Server) portalRoutes(mux *http.ServeMux) {
	portal.Register(mux, s.portal)
}

// docsRoutes serves the "Panduan" guides to any logged-in admin.
func (s *Server) docsRoutes(mux *http.ServeMux, all func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/docs", all(http.HandlerFunc(s.docsIndex)))
	mux.Handle("GET /admin/docs/{slug}", all(http.HandlerFunc(s.docsPage)))
}

func (s *Server) reportRoutes(mux *http.ServeMux, all, noReport, managers func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/reports", all(s.reportPage(false)))
	mux.Handle("GET /admin/reports/period", all(s.reportPage(true)))
	mux.Handle("GET /admin/reports/print", all(http.HandlerFunc(s.reportPrint)))
	mux.Handle("GET /admin/reports/export", all(http.HandlerFunc(s.reportExport)))
	mux.Handle("GET /admin/reports/usage.csv", managers(http.HandlerFunc(s.usageExport)))
	mux.Handle("GET /admin/transactions/{id}/invoice", noReport(http.HandlerFunc(s.trxInvoice)))
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

	// auth: root redirect, static files, uploads, admin login/logout, own password
	mux.HandleFunc("GET /{$}", s.root)
	mux.Handle("GET /static/", staticHandler(static))
	mux.HandleFunc("GET /admin/login", s.loginForm)
	mux.HandleFunc("GET /uploads/{name}", s.serveUpload)
	mux.HandleFunc("POST /admin/login", s.loginSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	mux.Handle("GET /admin/password", all(http.HandlerFunc(s.passwordForm)))
	mux.Handle("POST /admin/password", all(http.HandlerFunc(s.passwordSave)))
	s.totpRoutes(mux, all)

	// app manifests (public: the browser fetches them without cookies)
	mux.HandleFunc("GET /admin/manifest.webmanifest", s.manifest(" Admin", "/admin", "/admin/"))
	mux.HandleFunc("GET /portal/manifest.webmanifest", s.manifest("", "/portal", "/portal/"))

	// dashboard
	mux.Handle("GET /admin", all(http.HandlerFunc(s.dashboard)))
	mux.Handle("GET /admin/status", managers(http.HandlerFunc(s.statusPage)))
	mux.Handle("GET /admin/status.json", managers(http.HandlerFunc(s.statusJSON)))

	// customers: search, list, detail, edit, delete, actions, custom fields, maps
	mux.Handle("GET /admin/search", all(http.HandlerFunc(s.searchPage)))
	mux.Handle("GET /admin/search.json", noReport(http.HandlerFunc(s.searchJSON)))
	mux.Handle("POST /admin/customers/message", staff(http.HandlerFunc(s.custMessageForm)))
	s.extraRoutes(mux, managers)
	// Old customfield.php: custom fields CRUD (managers only).
	// F5 maps and ODP (maps.go, odp.go). Old PHP: customer map is open to all admins; router, ODP and their maps are managers only.

	// recharge and billing: recharge, deposit, customer plans, transactions, payment gateway admin
	mux.Handle("POST /admin/customers/{id}/recharge/confirm", staff(http.HandlerFunc(s.custRechargeConfirm)))
	mux.Handle("POST /admin/customers/{id}/recharge", staff(http.HandlerFunc(s.custRecharge)))
	mux.Handle("POST /admin/customers/recharge", staff(http.HandlerFunc(s.bulkRechargeConfirm)))
	mux.Handle("POST /admin/customers/recharge/run", staff(http.HandlerFunc(s.bulkRechargeRun)))
	mux.Handle("GET /admin/customers/recharge/result", staff(http.HandlerFunc(s.bulkRechargeResult)))
	mux.Handle("GET /admin/recharge", staff(http.HandlerFunc(s.rechargePick)))
	mux.Handle("POST /admin/recharge", staff(http.HandlerFunc(s.rechargeStart)))
	mux.Handle("GET /admin/deposit", money(http.HandlerFunc(s.depositForm)))
	mux.Handle("POST /admin/deposit", money(http.HandlerFunc(s.depositSave)))
	mux.Handle("GET /admin/subscriptions", all(http.HandlerFunc(s.subList)))
	mux.Handle("GET /admin/subscriptions/export", noReport(http.HandlerFunc(s.subExport)))
	mux.Handle("GET /admin/isolations", all(http.HandlerFunc(s.isoList)))
	mux.Handle("GET /admin/isolations/export", noReport(http.HandlerFunc(s.isoExport)))
	mux.Handle("GET /admin/subscriptions/{id}/edit", managers(http.HandlerFunc(s.subEdit)))
	mux.Handle("POST /admin/subscriptions/{id}", managers(http.HandlerFunc(s.subSave)))
	mux.Handle("POST /admin/subscriptions/{id}/extend", staff(http.HandlerFunc(s.subExtend)))
	mux.Handle("POST /admin/subscriptions/{id}/deactivate", managers(http.HandlerFunc(s.subDeactivate)))
	mux.Handle("POST /admin/subscriptions/{id}/sync", managers(http.HandlerFunc(s.subSync)))
	mux.Handle("GET /admin/transactions", noReport(http.HandlerFunc(s.trxList)))
	s.paymentAdminRoutes(mux, managers)

	// vouchers and coupons
	// Old PHP plan.php: voucher list is open to all admins, generate/redeem to staff, delete to managers.
	mux.Handle("GET /admin/vouchers", all(http.HandlerFunc(s.vchList)))
	mux.Handle("GET /admin/vouchers/new", money(http.HandlerFunc(s.vchNew)))
	mux.Handle("POST /admin/vouchers", money(http.HandlerFunc(s.vchGenerate)))
	mux.Handle("GET /admin/vouchers/print", staff(http.HandlerFunc(s.vchPrint)))
	mux.Handle("GET /admin/vouchers/view", staff(http.HandlerFunc(s.vchView)))
	mux.Handle("GET /admin/vouchers/redeem", staff(http.HandlerFunc(s.vchRedeemForm)))
	mux.Handle("POST /admin/vouchers/redeem", staff(http.HandlerFunc(s.vchRedeem)))
	mux.Handle("POST /admin/vouchers/delete-many", managers(http.HandlerFunc(s.vchDeleteMany)))
	mux.Handle("POST /admin/vouchers/remove-old", managers(http.HandlerFunc(s.vchRemoveOld)))
	mux.Handle("POST /admin/vouchers/{id}/delete", managers(http.HandlerFunc(s.vchDelete)))

	// agent consignment batches: managers write, an Agent reads its own
	agentRead := s.requireAdmin("SuperAdmin", "Admin", "Agent")
	mux.Handle("GET /admin/agent-batches", agentRead(http.HandlerFunc(s.abList)))
	mux.Handle("GET /admin/agent-batches/new", managers(http.HandlerFunc(s.abNew)))
	mux.Handle("POST /admin/agent-batches", managers(http.HandlerFunc(s.abCreate)))
	mux.Handle("GET /admin/agent-batches/{id}", agentRead(http.HandlerFunc(s.abView)))
	mux.Handle("GET /admin/agent-batches/{id}/payment", managers(http.HandlerFunc(s.abPayment)))
	mux.Handle("POST /admin/agent-batches/{id}/payment", managers(http.HandlerFunc(s.abPay)))
	mux.Handle("POST /admin/agent-batches/{id}/return", managers(http.HandlerFunc(s.abReturn)))
	// Old PHP coupons.php: every action is SuperAdmin/Admin/Sales only.
	cpn := s.requireAdmin("SuperAdmin", "Admin", "Sales")
	mux.Handle("GET /admin/coupons", cpn(http.HandlerFunc(s.cpnList)))
	mux.Handle("GET /admin/coupons/new", cpn(http.HandlerFunc(s.cpnNew)))
	mux.Handle("POST /admin/coupons", cpn(http.HandlerFunc(s.cpnSave)))
	mux.Handle("GET /admin/coupons/{id}/edit", cpn(http.HandlerFunc(s.cpnEdit)))
	mux.Handle("POST /admin/coupons/{id}", cpn(http.HandlerFunc(s.cpnSave)))
	mux.Handle("POST /admin/coupons/{id}/toggle", cpn(http.HandlerFunc(s.cpnToggle)))
	mux.Handle("POST /admin/coupons/delete-many", cpn(http.HandlerFunc(s.cpnDeleteMany)))
	mux.Handle("POST /admin/coupons/{id}/delete", cpn(http.HandlerFunc(s.cpnDelete)))

	// plans and network: bandwidth, plans, pools, routers, NAS, online sessions, ODP and router/ODP maps
	mux.Handle("GET /admin/routers/{id}/reconcile", managers(http.HandlerFunc(s.routerReconcile)))
	mux.Handle("POST /admin/routers/{id}/reconcile", managers(http.HandlerFunc(s.routerReconcile)))
	mux.Handle("GET /admin/network/{kind}/{id}", managers(http.HandlerFunc(s.networkDetail)))
	mux.Handle("POST /admin/network/{kind}/{id}/check", managers(http.HandlerFunc(s.networkCheck)))
	mux.Handle("POST /admin/radius/sessions/disconnect-many", managers(http.HandlerFunc(s.radiusDisconnectMany)))
	mux.Handle("POST /admin/radius/sessions/{id}/disconnect", managers(http.HandlerFunc(s.radiusDisconnect)))
	mux.Handle("GET /admin/radius/ip-lookup", managers(http.HandlerFunc(s.ipLookupForm)))
	mux.Handle("POST /admin/radius/ip-lookup", managers(http.HandlerFunc(s.ipLookupRun)))
	mux.Handle("GET /admin/radius/test-login", managers(http.HandlerFunc(s.testLoginForm)))
	mux.Handle("POST /admin/radius/test-login", managers(http.HandlerFunc(s.testLoginRun)))

	// reports and logs
	s.reportRoutes(mux, all, noReport, managers)
	s.docsRoutes(mux, all)
	mux.Handle("GET /admin/logs", managers(http.HandlerFunc(s.logList)))
	mux.Handle("GET /admin/logs/radius", managers(http.HandlerFunc(s.radiusLog)))
	mux.Handle("GET /admin/logs/radius/export", managers(http.HandlerFunc(s.radiusLogExport)))
	mux.Handle("GET /admin/logs/auth", managers(http.HandlerFunc(s.radiusAuthLog)))
	mux.Handle("GET /admin/logs/auth/export", managers(http.HandlerFunc(s.radiusAuthLogExport)))
	mux.Handle("GET /admin/logs/messages", managers(http.HandlerFunc(s.msgLog)))
	mux.Handle("GET /admin/logs/messages/export", managers(http.HandlerFunc(s.msgLogExport)))

	// settings: app settings, theme default, admin users, static pages
	mux.Handle("GET /admin/settings", managers(http.HandlerFunc(s.settingsForm)))
	mux.Handle("GET /admin/settings/{tab}", managers(http.HandlerFunc(s.settingsForm)))
	mux.Handle("POST /admin/settings/{tab}", managers(http.HandlerFunc(s.settingsSave)))
	mux.Handle("POST /admin/theme/default", s.requireAdmin("SuperAdmin")(http.HandlerFunc(s.themeDefault)))
	mux.Handle("POST /admin/settings/integrations/wa-test", managers(http.HandlerFunc(s.waTest)))
	mux.Handle("POST /admin/settings/notifications/daily-summary", managers(http.HandlerFunc(s.dailySummaryNow)))
	mux.Handle("POST /admin/settings/notifications/reset/{name}", managers(http.HandlerFunc(s.notifReset)))
	mux.Handle("GET /admin/settings/system/backup", managers(http.HandlerFunc(s.dbBackup)))
	// Promosi: SuperAdmin only
	promoOnly := s.requireAdmin("SuperAdmin")
	mux.Handle("GET /admin/settings/promo", promoOnly(http.HandlerFunc(s.promoList)))
	mux.Handle("GET /admin/settings/promo/new", promoOnly(http.HandlerFunc(s.promoNew)))
	mux.Handle("POST /admin/settings/promo/new", promoOnly(http.HandlerFunc(s.promoSave)))
	mux.Handle("GET /admin/settings/promo/{id}/edit", promoOnly(http.HandlerFunc(s.promoEdit)))
	mux.Handle("POST /admin/settings/promo/{id}", promoOnly(http.HandlerFunc(s.promoSave)))
	mux.Handle("POST /admin/settings/promo/{id}/delete", promoOnly(http.HandlerFunc(s.promoDelete)))
	mux.Handle("POST /admin/settings/promo/{id}/up", promoOnly(s.promoMove(-1)))
	mux.Handle("POST /admin/settings/promo/{id}/down", promoOnly(s.promoMove(1)))
	superAdmin := s.requireAdmin("SuperAdmin")
	s.setupRoutes(mux, superAdmin)
	mux.Handle("POST /admin/settings/integrations/test/{kind}", superAdmin(http.HandlerFunc(s.integrationTest)))
	mux.Handle("POST /admin/settings/payment/tripay-test", superAdmin(http.HandlerFunc(s.tripayTest)))
	mux.Handle("POST /admin/settings/payment/duitku-test", superAdmin(http.HandlerFunc(s.duitkuTest)))
	mux.Handle("POST /admin/settings/integrations/metrics-token", superAdmin(http.HandlerFunc(s.metricsTokenNew)))
	mux.Handle("POST /admin/settings/integrations/metrics-token/disable", superAdmin(http.HandlerFunc(s.metricsTokenOff)))
	mux.Handle("GET /admin/settings/isolir/preview", superAdmin(http.HandlerFunc(s.public.IsolirPreview)))
	mux.Handle("POST /admin/settings/isolir/reset/{section}", superAdmin(http.HandlerFunc(s.isolirReset)))
	mux.Handle("GET /admin/settings/system/import", superAdmin(http.HandlerFunc(s.importForm)))
	mux.Handle("POST /admin/settings/system/import", superAdmin(http.HandlerFunc(s.importPreview)))
	mux.Handle("POST /admin/settings/system/import/confirm", superAdmin(http.HandlerFunc(s.importConfirm)))
	mux.Handle("POST /admin/settings/system/import/cancel", superAdmin(http.HandlerFunc(s.importCancel)))
	mux.Handle("GET "+keyPage, superAdmin(http.HandlerFunc(s.keyForm)))
	mux.Handle("POST "+keyPage+"/saved", superAdmin(http.HandlerFunc(s.keySaved)))
	mux.Handle("POST "+keyPage+"/download", superAdmin(http.HandlerFunc(s.keyDownload)))
	mux.Handle("GET /admin/settings/system/restore", superAdmin(http.HandlerFunc(s.restoreForm)))
	mux.Handle("POST /admin/settings/system/restore", superAdmin(http.HandlerFunc(s.restorePreview)))
	mux.Handle("POST /admin/settings/system/restore/confirm", superAdmin(http.HandlerFunc(s.restoreConfirm)))
	mux.Handle("POST /admin/settings/system/restore/cancel", superAdmin(http.HandlerFunc(s.restoreCancel)))
	// Old settings.php users-*: list/add/edit for SuperAdmin, Admin and Agent (scoped in admins.go); delete only SuperAdmin and Admin.
	userMgr := s.requireAdmin("SuperAdmin", "Admin", "Agent")
	mux.Handle("GET /admin/users", userMgr(http.HandlerFunc(s.adminList)))
	mux.Handle("GET /admin/users/new", userMgr(http.HandlerFunc(s.adminNew)))
	mux.Handle("POST /admin/users", userMgr(http.HandlerFunc(s.adminSave)))
	mux.Handle("GET /admin/users/{id}/edit", userMgr(http.HandlerFunc(s.adminEdit)))
	mux.Handle("POST /admin/users/{id}", userMgr(http.HandlerFunc(s.adminSave)))
	mux.Handle("POST /admin/users/{id}/delete", managers(http.HandlerFunc(s.adminDelete)))
	mux.Handle("POST /admin/users/{id}/password/reset", s.requireAdmin("SuperAdmin")(http.HandlerFunc(s.adminPasswordReset)))
	// Old pages.php: static page editor (managers only).
	mux.Handle("GET /admin/pages/{slug}", managers(http.HandlerFunc(s.pageEdit)))
	mux.Handle("POST /admin/pages/{slug}", managers(http.HandlerFunc(s.pageSave)))

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
	top.HandleFunc("GET /metrics", s.metricsHandler)
	top.HandleFunc("GET /licenses", licenses)
	top.Handle("/", app)
	return cacheDefaults(top)
}
