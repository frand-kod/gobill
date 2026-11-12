package network

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

// Register adds the router, NAS, WireGuard, migration, live session, ODP and map routes.
func Register(mux *http.ServeMux, h *Handlers, m adminx.Mw) {
	mux.Handle("GET /admin/maps/customers", m.All(h.mapPage("Customer Geo Location Information", "/admin/maps/customers/data")))
	mux.Handle("GET /admin/maps/customers/data", m.All(http.HandlerFunc(h.mapCustomerData)))

	m.Crud(mux, "/admin/routers", h.routerList, h.routerNew, h.routerEdit, h.RouterSave, h.routerDelete)
	mux.Handle("POST /admin/routers/{id}/test", m.Managers(http.HandlerFunc(h.routerTest)))
	mux.Handle("POST /admin/routers/{id}/sync", m.Managers(http.HandlerFunc(h.routerSync)))
	mux.Handle("GET /admin/routers/{id}/sync", m.Managers(http.HandlerFunc(h.routerSyncStatus)))
	mux.Handle("GET /admin/routers/{id}/migration", m.SuperAdmin(http.HandlerFunc(h.routerMigration)))
	mux.Handle("POST /admin/routers/{id}/migration/{act}", m.SuperAdmin(http.HandlerFunc(h.routerMigrationAct)))
	mux.Handle("POST /admin/routers/{id}/wg/provision", m.SuperAdmin(http.HandlerFunc(h.routerWGProvision)))
	mux.Handle("POST /admin/routers/{id}/wg/revoke", m.SuperAdmin(http.HandlerFunc(h.routerWGRevoke)))
	mux.Handle("POST /admin/routers/{id}/wg/reset", m.SuperAdmin(http.HandlerFunc(h.routerWGReset)))
	mux.Handle("POST /admin/routers/{id}/wg/check", m.SuperAdmin(http.HandlerFunc(h.routerWGCheck)))
	mux.Handle("GET /admin/network/{kind}/{id}", m.Managers(http.HandlerFunc(h.networkDetail)))
	mux.Handle("POST /admin/network/{kind}/{id}/check", m.Managers(http.HandlerFunc(h.networkCheck)))
	m.Crud(mux, "/admin/nas", h.nasList, h.nasNew, h.nasEdit, h.NASSave, h.nasDelete)
	mux.Handle("GET /admin/radius/sessions", m.Managers(http.HandlerFunc(h.radiusSessions)))
	mux.Handle("GET /admin/radius/sessions.json", m.Managers(http.HandlerFunc(h.radiusSessionsJSON)))
	m.Crud(mux, "/admin/odp", h.odpList, h.odpNew, h.odpEdit, h.odpSave, h.odpDelete)
	mux.Handle("GET /admin/maps/routers", m.Managers(h.mapPage("Routers Geo Location Information", "/admin/maps/routers/data")))
	mux.Handle("GET /admin/maps/routers/data", m.Managers(http.HandlerFunc(h.mapRouterData)))
	mux.Handle("GET /admin/maps/odp", m.Managers(h.mapPage("ODP Geo Location Information", "/admin/maps/odp/data")))
	mux.Handle("GET /admin/maps/odp/data", m.Managers(http.HandlerFunc(h.mapODPData)))
	mux.Handle("GET /admin/routers/{id}/reconcile", m.Managers(http.HandlerFunc(h.routerReconcile)))
	mux.Handle("POST /admin/routers/{id}/reconcile", m.Managers(http.HandlerFunc(h.routerReconcile)))
	mux.Handle("POST /admin/radius/sessions/disconnect-many", m.Managers(http.HandlerFunc(h.radiusDisconnectMany)))
	mux.Handle("POST /admin/radius/sessions/{id}/disconnect", m.Managers(http.HandlerFunc(h.radiusDisconnect)))
	mux.Handle("GET /admin/radius/ip-lookup", m.Managers(http.HandlerFunc(h.ipLookupForm)))
	mux.Handle("POST /admin/radius/ip-lookup", m.Managers(http.HandlerFunc(h.ipLookupRun)))
	mux.Handle("GET /admin/radius/test-login", m.Managers(http.HandlerFunc(h.testLoginForm)))
	mux.Handle("POST /admin/radius/test-login", m.Managers(http.HandlerFunc(h.testLoginRun)))
}
