package plans

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

// Register adds the bandwidth, plan and pool routes. All are managers only (old PHP).
func Register(mux *http.ServeMux, h *Handlers, mw adminx.Mw) {
	mw.Crud(mux, "/admin/bandwidth", h.bwList, h.bwNew, h.bwEdit, h.BwSave, h.bwDelete)
	mw.Crud(mux, "/admin/plans", h.planList, h.planNew, h.planEdit, h.planSave, h.planDelete)
	mw.Crud(mux, "/admin/pool", h.poolList, h.poolNew, h.poolEdit, h.poolSave, h.poolDelete)
}
