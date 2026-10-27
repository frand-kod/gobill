package web

// Route registration: the main mux table and per-area route groups.

import (
	"io/fs"
	"log/slog"
	"net/http"

	"context"
	gobill "github.com/frand-kod/gobill"
	"strings"
)

// prefetchSafe is the session middleware with one change: a prefetch (Sec-Purpose, or Purpose on older
// builds) reads the session but never saves it, so a flash or a one-time value (metrics_token_once) survives
// for the real navigation. Responses vary on Sec-Purpose so a cached prefetch is never served as a navigation.
// ponytail: a prefetched page can be a few minutes old when shown; acceptable for intent-based prefetch.
func (s *Server) prefetchSafe(next http.Handler) http.Handler {
	saved := s.sessions.LoadAndSave(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Sec-Purpose")
		if !isPrefetch(r) {
			saved.ServeHTTP(w, r)
			return
		}
		token := ""
		if c, err := r.Cookie(s.sessions.Cookie.Name); err == nil {
			token = c.Value
		}
		ctx, err := s.sessions.Load(r.Context(), token)
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
	mux.Handle("POST /admin/customers/{id}/deactivate", managers(http.HandlerFunc(s.custDeactivate)))
	mux.Handle("POST /admin/customers/{id}/sync", managers(http.HandlerFunc(s.custSync)))
	mux.Handle("POST /admin/customers/{id}/login", managers(http.HandlerFunc(s.custLoginAs)))
	mux.Handle("POST /admin/logs/clean/{kind}", managers(http.HandlerFunc(s.logClean)))

	mux.HandleFunc("POST /portal/impersonate/end", s.pImpersonateEnd)
	mux.Handle("GET /portal/activation", s.requireCustomer(s.pActivations))
	mux.Handle("GET /portal/plans/{id}/friend", s.requireCustomer(s.pFriendForm))
	mux.Handle("POST /portal/plans/{id}/friend", s.requireCustomer(s.pFriend))
	mux.HandleFunc("GET /portal/forgot/username", s.pForgotUserForm)
	mux.HandleFunc("POST /portal/forgot/username", s.pForgotUser)
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

func (s *Server) inboxRoutes(mux *http.ServeMux) {
	mux.Handle("GET /portal/inbox", s.requireCustomer(s.pInbox))
	mux.Handle("GET /portal/inbox/{id}", s.requireCustomer(s.pInboxView))
}

func (s *Server) paymentRoutes(mux *http.ServeMux) {
	mux.Handle("POST /portal/plans/{id}/pay", s.requireCustomer(s.pPay))
	mux.Handle("POST /portal/topup", s.requireCustomer(s.pTopUp))
	mux.Handle("GET /portal/payments/{id}", s.requireCustomer(s.pPayView))
	mux.Handle("POST /portal/payments/{id}/check", s.requireCustomer(s.pPayCheck))
	mux.HandleFunc("POST /callback/tripay", s.tripayCallback)
	mux.HandleFunc("POST /callback/duitku", s.tripayCallback) // same handler: the configured gateway verifies it
	mux.HandleFunc("GET /qris/{id}/{token}", s.qrisPage)
}

// ---- admin (A71-A73) ----

func (s *Server) paymentAdminRoutes(mux *http.ServeMux, managers func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/payment-gateway", managers(http.HandlerFunc(s.pgList)))
	mux.Handle("GET /admin/payment-gateway/audit", managers(http.HandlerFunc(s.pgAudit)))
	mux.Handle("GET /admin/payment-gateway/audit/export", managers(http.HandlerFunc(s.pgAuditExport)))
	mux.Handle("GET /admin/payment-gateway/audit/{id}", managers(http.HandlerFunc(s.pgAuditView)))
}

func (s *Server) portalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", s.pLoginForm)
	mux.HandleFunc("POST /login", s.pLogin)
	mux.HandleFunc("POST /portal/logout", s.pLogout)
	mux.HandleFunc("POST /portal/login/activation", s.pVoucherLogin)
	mux.HandleFunc("GET /portal/register", s.pRegisterForm)
	mux.HandleFunc("POST /portal/register", s.pRegister)
	mux.Handle("GET /portal", s.requireCustomer(s.pDashboard))
	mux.Handle("GET /portal/profile", s.requireCustomer(s.pProfileForm))
	mux.Handle("POST /portal/profile", s.requireCustomer(s.pProfile))
	mux.Handle("POST /portal/password", s.requireCustomer(s.pPassword))
	mux.Handle("GET /portal/orders", s.requireCustomer(s.pOrders))
	mux.Handle("GET /portal/connection", s.requireCustomer(s.pConnHistory))
	mux.Handle("GET /portal/usage.json", s.requireCustomer(s.pUsageJSON))
	mux.Handle("GET /portal/orders/{id}/invoice", s.requireCustomer(s.pInvoice))
	mux.Handle("GET /portal/plans", s.requireCustomer(s.pPlans))
	mux.Handle("POST /portal/plans/{id}/balance", s.requireCustomer(s.pBuyBalance))
	s.inboxRoutes(mux)
	s.portalRoutes2(mux)
	mux.HandleFunc("GET /portal/forgot", s.pForgotForm)
	mux.HandleFunc("POST /portal/forgot", s.pForgotSend)
	mux.HandleFunc("POST /portal/forgot/verify", s.pForgotVerify)
	mux.HandleFunc("POST /portal/forgot/reset", s.pForgotReset)
	mux.HandleFunc("GET /pages/{slug}", s.pagePublic)
}

// Portal features that mirror home.php / voucher.php / accounts.php extras.

func (s *Server) portalRoutes2(mux *http.ServeMux) {
	mux.Handle("GET /portal/voucher", s.requireCustomer(s.pVoucherForm))
	mux.Handle("POST /portal/voucher", s.requireCustomer(s.pVoucher))
	mux.Handle("POST /portal/transfer", s.requireCustomer(s.pTransfer))
	mux.Handle("POST /portal/extend/{id}", s.requireCustomer(s.pExtend))
	mux.Handle("POST /portal/contact/{kind}/otp", s.requireCustomer(s.pContactOTP))
	mux.Handle("POST /portal/contact/{kind}/verify", s.requireCustomer(s.pContactVerify))
}

// radiusRestRoutes serves the FreeRADIUS rlm_rest endpoint of old phpnuxbill (radius.php), so
// an existing FreeRADIUS only needs a new connect_uri. Decisions come from radius.Server.
func (s *Server) radiusRestRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /radius.php", s.radiusRest)
	mux.HandleFunc("POST /radius/rest", s.radiusRest)
	if m, err := s.loadSettings(context.Background()); err == nil && strings.TrimSpace(m["radius_rest_allow"]) == "" {
		slog.Warn("radius.php endpoint accepts loopback only (radius_rest_allow is empty); if FreeRADIUS runs on another host, set radius_rest_allow to its IP (comma-separated IPs/CIDRs)")
	}
}

// docsRoutes serves the "Panduan" guides to any logged-in admin.
func (s *Server) docsRoutes(mux *http.ServeMux, all func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/docs", all(http.HandlerFunc(s.docsIndex)))
	mux.Handle("GET /admin/docs/{slug}", all(http.HandlerFunc(s.docsPage)))
}

func (s *Server) reportRoutes(mux *http.ServeMux, all func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/reports", all(s.reportPage(false)))
	mux.Handle("GET /admin/reports/period", all(s.reportPage(true)))
	mux.Handle("GET /admin/reports/print", all(http.HandlerFunc(s.reportPrint)))
	mux.Handle("GET /admin/reports/export", all(http.HandlerFunc(s.reportExport)))
	mux.Handle("GET /admin/transactions/{id}/invoice", all(http.HandlerFunc(s.trxInvoice)))
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
	crud := func(base string, list, nw, edit, save, del http.HandlerFunc) {
		mux.Handle("GET "+base, managers(list))
		mux.Handle("GET "+base+"/new", managers(nw))
		mux.Handle("POST "+base, managers(save))
		mux.Handle("GET "+base+"/{id}/edit", managers(edit))
		mux.Handle("POST "+base+"/{id}", managers(save))
		mux.Handle("POST "+base+"/{id}/delete", managers(del))
	}

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
	mux.Handle("GET /admin/search.json", all(http.HandlerFunc(s.searchJSON)))
	mux.Handle("GET /admin/customers/pick", staff(http.HandlerFunc(s.custPick)))
	mux.Handle("GET /admin/customers", all(http.HandlerFunc(s.custList)))
	mux.Handle("GET /admin/customers/export", all(http.HandlerFunc(s.custExport)))
	mux.Handle("GET /admin/customers/new", staff(http.HandlerFunc(s.custNew)))
	mux.Handle("POST /admin/customers", staff(http.HandlerFunc(s.custSave)))
	mux.Handle("GET /admin/customers/{id}", all(http.HandlerFunc(s.custView)))
	mux.Handle("GET /admin/customers/{id}/edit", managers(http.HandlerFunc(s.custEdit)))
	mux.Handle("POST /admin/customers/{id}", managers(http.HandlerFunc(s.custSave)))
	mux.Handle("POST /admin/customers/message", staff(http.HandlerFunc(s.custMessageForm)))
	mux.Handle("POST /admin/customers/{id}/delete", managers(http.HandlerFunc(s.custDelete)))
	s.extraRoutes(mux, managers)
	// Old customfield.php: custom fields CRUD (managers only).
	crud("/admin/fields", s.cfList, s.cfNew, s.cfEdit, s.cfSave, s.cfDelete)
	// F5 maps and ODP (maps.go, odp.go). Old PHP: customer map is open to all admins; router, ODP and their maps are managers only.
	mux.Handle("GET /admin/maps/customers", all(s.mapPage("Customer Geo Location Information", "/admin/maps/customers/data")))
	mux.Handle("GET /admin/maps/customers/data", all(http.HandlerFunc(s.mapCustomerData)))

	// recharge and billing: recharge, deposit, customer plans, transactions, payment gateway admin
	mux.Handle("POST /admin/customers/{id}/recharge/confirm", staff(http.HandlerFunc(s.custRechargeConfirm)))
	mux.Handle("POST /admin/customers/{id}/recharge", staff(http.HandlerFunc(s.custRecharge)))
	mux.Handle("POST /admin/customers/recharge", staff(http.HandlerFunc(s.bulkRechargeConfirm)))
	mux.Handle("POST /admin/customers/recharge/run", staff(http.HandlerFunc(s.bulkRechargeRun)))
	mux.Handle("GET /admin/customers/recharge/result", staff(http.HandlerFunc(s.bulkRechargeResult)))
	mux.Handle("GET /admin/customers/{id}/summary", staff(http.HandlerFunc(s.custSummary)))
	mux.Handle("GET /admin/recharge", staff(http.HandlerFunc(s.rechargePick)))
	mux.Handle("POST /admin/recharge", staff(http.HandlerFunc(s.rechargeStart)))
	mux.Handle("GET /admin/deposit", staff(http.HandlerFunc(s.depositForm)))
	mux.Handle("POST /admin/deposit", staff(http.HandlerFunc(s.depositSave)))
	mux.Handle("GET /admin/subscriptions", all(http.HandlerFunc(s.subList)))
	mux.Handle("GET /admin/subscriptions/export", all(http.HandlerFunc(s.subExport)))
	mux.Handle("GET /admin/isolations", all(http.HandlerFunc(s.isoList)))
	mux.Handle("GET /admin/isolations/export", all(http.HandlerFunc(s.isoExport)))
	mux.Handle("GET /admin/subscriptions/{id}/edit", managers(http.HandlerFunc(s.subEdit)))
	mux.Handle("POST /admin/subscriptions/{id}", managers(http.HandlerFunc(s.subSave)))
	mux.Handle("POST /admin/subscriptions/{id}/extend", staff(http.HandlerFunc(s.subExtend)))
	mux.Handle("POST /admin/subscriptions/{id}/deactivate", managers(http.HandlerFunc(s.subDeactivate)))
	mux.Handle("POST /admin/subscriptions/{id}/sync", managers(http.HandlerFunc(s.subSync)))
	mux.Handle("GET /admin/transactions", all(http.HandlerFunc(s.trxList)))
	s.paymentAdminRoutes(mux, managers)

	// vouchers and coupons
	// Old PHP plan.php: voucher list is open to all admins, generate/redeem to staff, delete to managers.
	mux.Handle("GET /admin/vouchers", all(http.HandlerFunc(s.vchList)))
	mux.Handle("GET /admin/vouchers/new", staff(http.HandlerFunc(s.vchNew)))
	mux.Handle("POST /admin/vouchers", staff(http.HandlerFunc(s.vchGenerate)))
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
	crud("/admin/bandwidth", s.bwList, s.bwNew, s.bwEdit, s.bwSave, s.bwDelete)
	crud("/admin/plans", s.planList, s.planNew, s.planEdit, s.planSave, s.planDelete)
	crud("/admin/pool", s.poolList, s.poolNew, s.poolEdit, s.poolSave, s.poolDelete)
	crud("/admin/routers", s.routerList, s.routerNew, s.routerEdit, s.routerSave, s.routerDelete)
	mux.Handle("POST /admin/routers/{id}/test", managers(http.HandlerFunc(s.routerTest)))
	crud("/admin/nas", s.nasList, s.nasNew, s.nasEdit, s.nasSave, s.nasDelete)
	mux.Handle("GET /admin/network/{kind}/{id}", managers(http.HandlerFunc(s.networkDetail)))
	mux.Handle("POST /admin/network/{kind}/{id}/check", managers(http.HandlerFunc(s.networkCheck)))
	mux.Handle("GET /admin/radius/sessions", managers(http.HandlerFunc(s.radiusSessions)))
	mux.Handle("GET /admin/radius/sessions.json", managers(http.HandlerFunc(s.radiusSessionsJSON)))
	mux.Handle("POST /admin/radius/sessions/disconnect-many", managers(http.HandlerFunc(s.radiusDisconnectMany)))
	mux.Handle("POST /admin/radius/sessions/{id}/disconnect", managers(http.HandlerFunc(s.radiusDisconnect)))
	crud("/admin/odp", s.odpList, s.odpNew, s.odpEdit, s.odpSave, s.odpDelete)
	mux.Handle("GET /admin/maps/routers", managers(s.mapPage("Routers Geo Location Information", "/admin/maps/routers/data")))
	mux.Handle("GET /admin/maps/routers/data", managers(http.HandlerFunc(s.mapRouterData)))
	mux.Handle("GET /admin/maps/odp", managers(s.mapPage("ODP Geo Location Information", "/admin/maps/odp/data")))
	mux.Handle("GET /admin/maps/odp/data", managers(http.HandlerFunc(s.mapODPData)))

	// reports and logs
	s.reportRoutes(mux, all)
	s.docsRoutes(mux, all)
	mux.Handle("GET /admin/logs", managers(http.HandlerFunc(s.logList)))
	mux.Handle("GET /admin/logs/radius", managers(http.HandlerFunc(s.radiusLog)))
	mux.Handle("GET /admin/logs/radius/export", managers(http.HandlerFunc(s.radiusLogExport)))
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
	mux.Handle("POST /admin/settings/integrations/metrics-token", superAdmin(http.HandlerFunc(s.metricsTokenNew)))
	mux.Handle("POST /admin/settings/integrations/metrics-token/disable", superAdmin(http.HandlerFunc(s.metricsTokenOff)))
	mux.Handle("GET /admin/settings/isolir/preview", superAdmin(http.HandlerFunc(s.isolirPreview)))
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
	s.paymentRoutes(mux)

	// RADIUS REST endpoint and payment callbacks (no session; bypass the origin check below)
	s.radiusRestRoutes(mux)

	// ISOLIR page for expired customers: public, identified by client IP; the voucher form posts
	// from the sandboxed custom page (origin null), so it bypasses the origin check below
	mux.HandleFunc("GET /isolir", s.isolirPage)
	mux.HandleFunc("POST /isolir/voucher", s.isolirVoucher)

	// the Tripay server posts the callback without our origin; its HMAC signature authenticates it
	cop := http.NewCrossOriginProtection()
	cop.AddInsecureBypassPattern("POST /callback/tripay")
	cop.AddInsecureBypassPattern("POST /callback/duitku")
	cop.AddInsecureBypassPattern("POST /isolir/voucher")
	cop.AddInsecureBypassPattern("POST /radius.php")
	cop.AddInsecureBypassPattern("POST /radius/rest")
	// isolirGate sits inside realIP (it needs the real client IP) and outside the session chain
	app := s.realIP(s.isolirGate(cop.Handler(s.prefetchSafe(s.idleGuard(s.maintenance(mux))))))
	// /health sits outside the session and maintenance chain so monitors always get an answer
	top := http.NewServeMux()
	top.HandleFunc("GET /health", s.health)
	top.HandleFunc("GET /metrics", s.metricsHandler)
	top.HandleFunc("GET /licenses", licenses)
	top.Handle("/", app)
	return cacheDefaults(top)
}
