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
