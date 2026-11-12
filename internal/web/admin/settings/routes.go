package settings

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

// Register adds the settings, admin user, two-factor, key, backup, restore, import, promotion and page routes.
func Register(mux *http.ServeMux, h *Handlers, m adminx.Mw) {
	all, managers, superAdmin := m.All, m.Managers, m.SuperAdmin
	mux.Handle("GET /admin/password", all(http.HandlerFunc(h.passwordForm)))
	mux.Handle("POST /admin/password", all(http.HandlerFunc(h.passwordSave)))
	// admin two-factor setup (the login step is in web)
	mux.Handle("GET /admin/2fa", all(http.HandlerFunc(h.admin2FAForm)))
	mux.Handle("POST /admin/2fa/setup", all(http.HandlerFunc(h.admin2FASetup)))
	mux.Handle("POST /admin/2fa/confirm", all(http.HandlerFunc(h.admin2FAConfirm)))
	mux.Handle("POST /admin/2fa/disable", all(http.HandlerFunc(h.admin2FADisable)))
	mux.Handle("POST /admin/users/{id}/2fa/reset", superAdmin(http.HandlerFunc(h.adminTOTPReset)))

	// settings: app settings, theme default, admin users, static pages
	mux.Handle("GET /admin/settings", managers(http.HandlerFunc(h.settingsForm)))
	mux.Handle("GET /admin/settings/{tab}", managers(http.HandlerFunc(h.settingsForm)))
	mux.Handle("POST /admin/settings/{tab}", managers(http.HandlerFunc(h.settingsSave)))
	mux.Handle("POST /admin/theme/default", superAdmin(http.HandlerFunc(h.themeDefault)))
	mux.Handle("POST /admin/settings/integrations/wa-test", managers(http.HandlerFunc(h.waTest)))
	mux.Handle("POST /admin/settings/notifications/daily-summary", managers(http.HandlerFunc(h.dailySummaryNow)))
	mux.Handle("POST /admin/settings/notifications/reset/{name}", managers(http.HandlerFunc(h.notifReset)))
	mux.Handle("GET /admin/settings/system/backup", managers(http.HandlerFunc(h.DbBackup)))
	// Promosi: SuperAdmin only
	mux.Handle("GET /admin/settings/promo", superAdmin(http.HandlerFunc(h.promoList)))
	mux.Handle("GET /admin/settings/promo/new", superAdmin(http.HandlerFunc(h.promoNew)))
	mux.Handle("POST /admin/settings/promo/new", superAdmin(http.HandlerFunc(h.promoSave)))
	mux.Handle("GET /admin/settings/promo/{id}/edit", superAdmin(http.HandlerFunc(h.promoEdit)))
	mux.Handle("POST /admin/settings/promo/{id}", superAdmin(http.HandlerFunc(h.promoSave)))
	mux.Handle("POST /admin/settings/promo/{id}/delete", superAdmin(http.HandlerFunc(h.promoDelete)))
	mux.Handle("POST /admin/settings/promo/{id}/up", superAdmin(h.promoMove(-1)))
	mux.Handle("POST /admin/settings/promo/{id}/down", superAdmin(h.promoMove(1)))
	mux.Handle("POST /admin/settings/integrations/test/{kind}", superAdmin(http.HandlerFunc(h.integrationTest)))
	mux.Handle("POST /admin/settings/payment/tripay-test", superAdmin(http.HandlerFunc(h.tripayTest)))
	mux.Handle("POST /admin/settings/payment/duitku-test", superAdmin(http.HandlerFunc(h.duitkuTest)))
	mux.Handle("POST /admin/settings/isolir/reset/{section}", superAdmin(http.HandlerFunc(h.isolirReset)))
	mux.Handle("GET /admin/settings/system/import", superAdmin(http.HandlerFunc(h.importForm)))
	mux.Handle("POST /admin/settings/system/import", superAdmin(http.HandlerFunc(h.importPreview)))
	mux.Handle("POST /admin/settings/system/import/confirm", superAdmin(http.HandlerFunc(h.importConfirm)))
	mux.Handle("POST /admin/settings/system/import/cancel", superAdmin(http.HandlerFunc(h.importCancel)))
	mux.Handle("GET "+KeyPage, superAdmin(http.HandlerFunc(h.keyForm)))
	mux.Handle("POST "+KeyPage+"/saved", superAdmin(http.HandlerFunc(h.keySaved)))
	mux.Handle("POST "+KeyPage+"/download", superAdmin(http.HandlerFunc(h.keyDownload)))
	mux.Handle("GET /admin/settings/system/restore", superAdmin(http.HandlerFunc(h.restoreForm)))
	mux.Handle("POST /admin/settings/system/restore", superAdmin(http.HandlerFunc(h.restorePreview)))
	mux.Handle("POST /admin/settings/system/restore/confirm", superAdmin(http.HandlerFunc(h.restoreConfirm)))
	mux.Handle("POST /admin/settings/system/restore/cancel", superAdmin(http.HandlerFunc(h.restoreCancel)))
	// Old settings.php users-*: list/add/edit for SuperAdmin, Admin and Agent (scoped in admin_users.go); delete only SuperAdmin and Admin.
	userMgr := m.Require("SuperAdmin", "Admin", "Agent")
	mux.Handle("GET /admin/users", userMgr(http.HandlerFunc(h.adminList)))
	mux.Handle("GET /admin/users/new", userMgr(http.HandlerFunc(h.adminNew)))
	mux.Handle("POST /admin/users", userMgr(http.HandlerFunc(h.adminSave)))
	mux.Handle("GET /admin/users/{id}/edit", userMgr(http.HandlerFunc(h.adminEdit)))
	mux.Handle("POST /admin/users/{id}", userMgr(http.HandlerFunc(h.adminSave)))
	mux.Handle("POST /admin/users/{id}/delete", managers(http.HandlerFunc(h.adminDelete)))
	mux.Handle("POST /admin/users/{id}/password/reset", superAdmin(http.HandlerFunc(h.adminPasswordReset)))
	// Old pages.php: static page editor (managers only).
	mux.Handle("GET /admin/pages/{slug}", managers(http.HandlerFunc(h.pageEdit)))
	mux.Handle("POST /admin/pages/{slug}", managers(http.HandlerFunc(h.pageSave)))
}
