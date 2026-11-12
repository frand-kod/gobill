package web

// The old lower-case names of what moved to web/admin/adminx, so the handlers that still live here read as before.

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

type (
	field      = adminx.Field
	formPage   = adminx.FormPage
	group      = adminx.Group
	option     = adminx.Option
	listRow    = adminx.ListRow
	listPage   = adminx.ListPage
	bulkAction = adminx.BulkAction
	rowAction  = adminx.RowAction
	filter     = adminx.Filter
)

const (
	msgRouterTimeout = adminx.MsgRouterTimeout
	msgRouterRefused = adminx.MsgRouterRefused
	msgRouterHost    = adminx.MsgRouterHost
	msgRouterLogin   = adminx.MsgRouterLogin
	msgRouterOther   = adminx.MsgRouterOther
	msgNASOther      = adminx.MsgNASOther
	msgPlanMissing   = adminx.MsgPlanMissing
	msgPlanOff       = adminx.MsgPlanOff
	msgPlanBad       = adminx.MsgPlanBad
	msgNoBalance     = adminx.MsgNoBalance
	msgRechargeFail  = adminx.MsgRechargeFail
	msgNotSynced     = adminx.MsgNotSynced
)

var (
	text         = adminx.Text
	section      = adminx.Section
	customerPick = adminx.CustomerPick
	wholeErr     = adminx.WholeErr
	row          = adminx.Row
	listSort     = adminx.ListSort
	jsonStr      = adminx.JSONStr
	routerErr    = adminx.RouterErr
	planErrMsg   = adminx.PlanErrMsg
)

// Forwarders to the shared admin helpers, so the handlers that still live here keep their calls.

func (s *Server) renderList(w http.ResponseWriter, r *http.Request, lp listPage) {
	s.ax.RenderList(w, r, lp)
}

func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, status int, fp formPage) {
	s.ax.RenderForm(w, r, status, fp)
}

func (s *Server) done(w http.ResponseWriter, r *http.Request, back, msg, action, desc string) {
	s.ax.Done(w, r, back, msg, action, desc)
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request, back, what, inUse, name string, del func(id int64) error) {
	s.ax.Remove(w, r, back, what, inUse, name, del)
}

func (s *Server) bulkDelete(w http.ResponseWriter, r *http.Request, back, what, inUse string, del func(q *db.Queries, ids []int64) (int64, error)) {
	s.ax.BulkDelete(w, r, back, what, inUse, del)
}

func (s *Server) bulkDone(w http.ResponseWriter, r *http.Request, back, action string, n int64) {
	s.ax.BulkDone(w, r, back, action, n)
}

func (s *Server) logActivity(r *http.Request, action, desc string) { s.ax.LogActivity(r, action, desc) }

func (s *Server) putFailure(r *http.Request, msg string, raw error) { s.ax.PutFailure(r, msg, raw) }
