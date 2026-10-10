package web

// Route registration: the main mux table and per-area route groups.

import (
	"io/fs"
	"log/slog"
	"net/http"

	"context"
	nuxbill "github.com/frand-kod/gobill"
	"strings"
)

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
	mux.HandleFunc("GET /portal/login", s.pLoginForm)
	mux.HandleFunc("POST /portal/login", s.pLogin)
	mux.HandleFunc("POST /portal/logout", s.pLogout)
	mux.HandleFunc("POST /portal/login/activation", s.pVoucherLogin)
	mux.HandleFunc("GET /portal/register", s.pRegisterForm)
	mux.HandleFunc("POST /portal/register", s.pRegister)
	mux.Handle("GET /portal", s.requireCustomer(s.pDashboard))
	mux.Handle("GET /portal/profile", s.requireCustomer(s.pProfileForm))
	mux.Handle("POST /portal/profile", s.requireCustomer(s.pProfile))
	mux.Handle("POST /portal/password", s.requireCustomer(s.pPassword))
	mux.Handle("GET /portal/orders", s.requireCustomer(s.pOrders))
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
	static, err := fs.Sub(nuxbill.FS, "web/static")
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
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("GET /uploads/{name}", s.serveUpload)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	mux.Handle("GET /admin/password", all(http.HandlerFunc(s.passwordForm)))
	mux.Handle("POST /admin/password", all(http.HandlerFunc(s.passwordSave)))

	// app manifests (public: the browser fetches them without cookies)
	mux.HandleFunc("GET /admin/manifest.webmanifest", s.manifest(" Admin", "/admin", "/admin/"))
	mux.HandleFunc("GET /portal/manifest.webmanifest", s.manifest("", "/portal", "/portal/"))

	// dashboard
	mux.Handle("GET /admin", all(http.HandlerFunc(s.dashboard)))

	// customers: search, list, detail, edit, delete, actions, custom fields, maps
	mux.Handle("GET /admin/search", staff(http.HandlerFunc(s.custSearch)))
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
	mux.Handle("GET /admin/customers/{id}/summary", staff(http.HandlerFunc(s.custSummary)))
	mux.Handle("GET /admin/recharge", staff(http.HandlerFunc(s.rechargePick)))
	mux.Handle("POST /admin/recharge", staff(http.HandlerFunc(s.rechargeStart)))
	mux.Handle("GET /admin/deposit", staff(http.HandlerFunc(s.depositForm)))
	mux.Handle("POST /admin/deposit", staff(http.HandlerFunc(s.depositSave)))
	mux.Handle("GET /admin/subscriptions", all(http.HandlerFunc(s.subList)))
	mux.Handle("GET /admin/subscriptions/export", all(http.HandlerFunc(s.subExport)))
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
	mux.Handle("GET /admin/vouchers/print", all(http.HandlerFunc(s.vchPrint)))
	mux.Handle("GET /admin/vouchers/view", staff(http.HandlerFunc(s.vchView)))
	mux.Handle("GET /admin/vouchers/redeem", staff(http.HandlerFunc(s.vchRedeemForm)))
	mux.Handle("POST /admin/vouchers/redeem", staff(http.HandlerFunc(s.vchRedeem)))
	mux.Handle("POST /admin/vouchers/delete-many", managers(http.HandlerFunc(s.vchDeleteMany)))
	mux.Handle("POST /admin/vouchers/remove-old", managers(http.HandlerFunc(s.vchRemoveOld)))
	mux.Handle("POST /admin/vouchers/{id}/delete", managers(http.HandlerFunc(s.vchDelete)))
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
	mux.Handle("GET /admin/settings/miscellaneous/backup", managers(http.HandlerFunc(s.dbBackup)))
	superAdmin := s.requireAdmin("SuperAdmin")
	mux.Handle("GET /admin/settings/miscellaneous/import", superAdmin(http.HandlerFunc(s.importForm)))
	mux.Handle("POST /admin/settings/miscellaneous/import", superAdmin(http.HandlerFunc(s.importPreview)))
	mux.Handle("POST /admin/settings/miscellaneous/import/confirm", superAdmin(http.HandlerFunc(s.importConfirm)))
	mux.Handle("POST /admin/settings/miscellaneous/import/cancel", superAdmin(http.HandlerFunc(s.importCancel)))
	mux.Handle("GET /admin/settings/miscellaneous/restore", superAdmin(http.HandlerFunc(s.restoreForm)))
	mux.Handle("POST /admin/settings/miscellaneous/restore", superAdmin(http.HandlerFunc(s.restorePreview)))
	mux.Handle("POST /admin/settings/miscellaneous/restore/confirm", superAdmin(http.HandlerFunc(s.restoreConfirm)))
	mux.Handle("POST /admin/settings/miscellaneous/restore/cancel", superAdmin(http.HandlerFunc(s.restoreCancel)))
	// Old settings.php users-*: list/add/edit for SuperAdmin, Admin and Agent (scoped in admins.go); delete only SuperAdmin and Admin.
	userMgr := s.requireAdmin("SuperAdmin", "Admin", "Agent")
	mux.Handle("GET /admin/users", userMgr(http.HandlerFunc(s.adminList)))
	mux.Handle("GET /admin/users/new", userMgr(http.HandlerFunc(s.adminNew)))
	mux.Handle("POST /admin/users", userMgr(http.HandlerFunc(s.adminSave)))
	mux.Handle("GET /admin/users/{id}/edit", userMgr(http.HandlerFunc(s.adminEdit)))
	mux.Handle("POST /admin/users/{id}", userMgr(http.HandlerFunc(s.adminSave)))
	mux.Handle("POST /admin/users/{id}/delete", managers(http.HandlerFunc(s.adminDelete)))
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

	// the Tripay server posts the callback without our origin; its HMAC signature authenticates it
	cop := http.NewCrossOriginProtection()
	cop.AddInsecureBypassPattern("POST /callback/tripay")
	cop.AddInsecureBypassPattern("POST /radius.php")
	cop.AddInsecureBypassPattern("POST /radius/rest")
	return s.realIP(cop.Handler(s.sessions.LoadAndSave(s.idleGuard(s.maintenance(mux)))))
}
