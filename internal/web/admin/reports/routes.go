package reports

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

// Register adds the dashboard, report, transaction, log, payment audit, status, search and metrics token routes.
func Register(mux *http.ServeMux, h *Handlers, m adminx.Mw) {
	all, noReport, managers, superAdmin := m.All, m.NoReport, m.Managers, m.SuperAdmin
	mux.Handle("GET /admin", all(http.HandlerFunc(h.dashboard)))
	mux.Handle("GET /admin/status", managers(http.HandlerFunc(h.statusPage)))
	mux.Handle("GET /admin/status.json", managers(http.HandlerFunc(h.statusJSON)))

	mux.Handle("GET /admin/search", all(http.HandlerFunc(h.searchPage)))
	mux.Handle("GET /admin/search.json", noReport(http.HandlerFunc(h.searchJSON)))

	mux.Handle("GET /admin/transactions", noReport(http.HandlerFunc(h.trxList)))
	mux.Handle("GET /admin/payment-gateway", managers(http.HandlerFunc(h.pgList)))
	mux.Handle("GET /admin/payment-gateway/audit", managers(http.HandlerFunc(h.pgAudit)))
	mux.Handle("GET /admin/payment-gateway/audit/export", managers(http.HandlerFunc(h.pgAuditExport)))
	mux.Handle("GET /admin/payment-gateway/audit/{id}", managers(http.HandlerFunc(h.pgAuditView)))

	mux.Handle("GET /admin/reports", all(h.reportPage(false)))
	mux.Handle("GET /admin/reports/period", all(h.reportPage(true)))
	mux.Handle("GET /admin/reports/print", all(http.HandlerFunc(h.reportPrint)))
	mux.Handle("GET /admin/reports/export", all(http.HandlerFunc(h.ReportExport)))
	mux.Handle("GET /admin/reports/usage.csv", managers(http.HandlerFunc(h.usageExport)))
	mux.Handle("GET /admin/transactions/{id}/invoice", noReport(http.HandlerFunc(h.trxInvoice)))

	mux.Handle("GET /admin/logs", managers(http.HandlerFunc(h.logList)))
	mux.Handle("GET /admin/logs/radius", managers(http.HandlerFunc(h.radiusLog)))
	mux.Handle("GET /admin/logs/radius/export", managers(http.HandlerFunc(h.radiusLogExport)))
	mux.Handle("GET /admin/logs/auth", managers(http.HandlerFunc(h.radiusAuthLog)))
	mux.Handle("GET /admin/logs/auth/export", managers(http.HandlerFunc(h.radiusAuthLogExport)))
	mux.Handle("GET /admin/logs/messages", managers(http.HandlerFunc(h.msgLog)))
	mux.Handle("GET /admin/logs/messages/export", managers(http.HandlerFunc(h.msgLogExport)))
	mux.Handle("POST /admin/logs/clean/{kind}", managers(http.HandlerFunc(h.logClean)))

	mux.Handle("POST /admin/settings/integrations/metrics-token", superAdmin(http.HandlerFunc(h.metricsTokenNew)))
	mux.Handle("POST /admin/settings/integrations/metrics-token/disable", superAdmin(http.HandlerFunc(h.metricsTokenOff)))
}
