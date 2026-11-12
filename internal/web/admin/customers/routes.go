package customers

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

// Register adds the customer routes. Old PHP: customers are readable by everyone, creatable by Agent and
// Sales too, editable and deletable by managers; custom fields are managers only.
func Register(mux *http.ServeMux, h *Handlers, mw adminx.Mw) {
	all, managers, staff, noReport := mw.All, mw.Managers, mw.Staff, mw.NoReport
	mux.Handle("GET /admin/customers/pick", staff(http.HandlerFunc(h.custPick)))
	mux.Handle("GET /admin/customers", all(http.HandlerFunc(h.custList)))
	mux.Handle("GET /admin/customers/export", noReport(http.HandlerFunc(h.custExport)))
	mux.Handle("GET /admin/customers/new", staff(http.HandlerFunc(h.custNew)))
	mux.Handle("POST /admin/customers", staff(http.HandlerFunc(h.custSave)))
	mux.Handle("GET /admin/customers/{id}", all(http.HandlerFunc(h.custView)))
	mux.Handle("GET /admin/customers/{id}/edit", managers(http.HandlerFunc(h.custEdit)))
	mux.Handle("POST /admin/customers/{id}", managers(http.HandlerFunc(h.custSave)))
	mux.Handle("POST /admin/customers/{id}/delete", managers(http.HandlerFunc(h.custDelete)))
	mux.Handle("POST /admin/customers/{id}/deactivate", managers(http.HandlerFunc(h.custDeactivate)))
	mux.Handle("POST /admin/customers/{id}/sync", managers(http.HandlerFunc(h.custSync)))
	mux.Handle("POST /admin/customers/{id}/usage-recount", managers(http.HandlerFunc(h.custRecountUsage)))
	mux.Handle("GET /admin/customers/{id}/summary", staff(http.HandlerFunc(h.custSummary)))
	mw.Crud(mux, "/admin/fields", h.cfList, h.cfNew, h.cfEdit, h.cfSave, h.cfDelete)
	mux.Handle("POST /admin/customers/{id}/login", mw.Managers(http.HandlerFunc(h.custLoginAs)))
	mux.HandleFunc("POST /portal/impersonate/end", h.pImpersonateEnd)
	mux.Handle("GET /admin/message/send", mw.Staff(http.HandlerFunc(h.msgSendForm)))
	mux.Handle("POST /admin/message/send", mw.Staff(http.HandlerFunc(h.msgSend)))
	mux.Handle("GET /admin/message/bulk", mw.Staff(http.HandlerFunc(h.msgBulkForm)))
	mux.Handle("POST /admin/message/bulk", mw.Staff(http.HandlerFunc(h.msgBulkStart)))
	mux.Handle("POST /admin/message/selected", mw.Staff(http.HandlerFunc(h.msgSelectedSend)))
	mux.Handle("GET /admin/message/bulk/status", mw.Staff(http.HandlerFunc(h.msgBulkStatus)))
	mux.Handle("POST /admin/customers/message", mw.Staff(http.HandlerFunc(h.custMessageForm)))
}
