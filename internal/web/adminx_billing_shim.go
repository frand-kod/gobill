package web

// Forwarders to the shared helpers in web/admin/adminx, for the handlers still in this package.

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

var (
	anyOpts    = adminx.AnyOpts
	titleWords = adminx.TitleWords
)

func (s *Server) custGet(w http.ResponseWriter, r *http.Request) (db.Customer, bool) {
	return s.ax.CustGet(w, r)
}

func (s *Server) summaryLabels() string { return s.ax.SummaryLabels() }

func (s *Server) planOptions(r *http.Request, enabledOnly bool) ([]option, map[int64]db.Plan, error) {
	return s.ax.PlanOptions(r, enabledOnly)
}
