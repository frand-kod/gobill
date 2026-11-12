package reports

// Payment request audit list, export and detail.

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

func (s *Handlers) pgList(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "payment gateways", err)
		return
	}
	rows := []listRow{{ID: 1, Cells: []string{"Tripay", "Disabled"}}, {ID: 2, Cells: []string{"Duitku", "Disabled"}}}
	for i, g := range []string{"tripay", "duitku"} {
		if st["payment_gateway"] == g {
			rows[i].Cells[1] = "Enabled"
		}
	}
	lp := listPage{Heading: "Online payments", Base: "/admin/payment-gateway", Cols: []string{"Gateway", "Status"},
		Rows:  rows,
		Links: []option{{Value: "/admin/settings/payment", Label: "Settings"}, {Value: "/admin/payment-gateway/audit", Label: "Payment Audit"}}}
	s.RenderList(w, r, lp)
}

var pgAuditCols = []string{"Transaction ID", "Gateway Ref", "Username", "Package Name", "Package Price", "Channel", "Status", "Created On", "Expires On", "Paid Date"}

func (s *Handlers) pgAuditParams(r *http.Request) db.SearchPaymentRequestsParams {
	_, _, f, t := s.DateRange(r)
	return db.SearchPaymentRequestsParams{Q: strings.TrimSpace(r.URL.Query().Get("q")), Status: r.URL.Query().Get("status"), FromTs: f, ToTs: t}
}

func (s *Handlers) pgAuditRows(rs []db.SearchPaymentRequestsRow) [][]string {
	out := [][]string{}
	for _, x := range rs {
		paid := ""
		if x.PaidAt.Valid {
			paid = s.TS(x.PaidAt.Int64)
		}
		out = append(out, []string{x.Ref, x.GatewayRef, x.Username, x.PlanName, view.Money(x.Amount), x.Channel, x.Status, s.TS(x.CreatedAt), s.TS(x.ExpiresAt), paid})
	}
	return out
}

var pgStatuses = []option{{Value: "", Label: "Status"}, {Value: "pending", Label: "pending"}, {Value: "paid", Label: "paid"}, {Value: "failed", Label: "failed"}, {Value: "expired", Label: "expired"}}

func (s *Handlers) pgAudit(w http.ResponseWriter, r *http.Request) {
	q, pg := view.Paging(r)
	p := s.pgAuditParams(r)
	p.PageLimit, p.PageOffset = pg.Limit(), pg.Offset()
	rs, err := s.Queries.SearchPaymentRequests(r.Context(), p)
	if err != nil {
		s.Fail(w, "payment audit", err)
		return
	}
	from, to, _, _ := s.DateRange(r)
	lp := listPage{Heading: "Payment Audit", Base: "/admin/payment-gateway/audit", Q: q, Searchable: true, Dates: true, From: from, To: to,
		Cols: pgAuditCols, ViewLink: true, Filters: []filter{{Name: "status", Val: p.Status, Opts: pgStatuses, Label: "Status"}}}
	for i, c := range s.pgAuditRows(rs) {
		lp.Rows = append(lp.Rows, listRow{ID: rs[i].ID, Cells: c})
	}
	lp.Links = []option{{Value: "/admin/payment-gateway/audit/export?" + lp.Query().Encode(), Label: "Export CSV"}}
	total, err := s.Queries.CountPaymentRequests(r.Context(), db.CountPaymentRequestsParams{Q: p.Q, Status: p.Status, FromTs: p.FromTs, ToTs: p.ToTs})
	if err != nil {
		s.Fail(w, "count payments", err)
		return
	}
	lp.Finish(pg, total)
	s.RenderList(w, r, lp)
}

func (s *Handlers) pgAuditExport(w http.ResponseWriter, r *http.Request) {
	p := s.pgAuditParams(r)
	p.PageLimit = -1
	rs, err := s.Queries.SearchPaymentRequests(r.Context(), p)
	if err != nil {
		s.Fail(w, "export payment audit", err)
		return
	}
	s.WriteCSV(w, "payment-audit", pgAuditCols, s.pgAuditRows(rs))
}

func (s *Handlers) pgAuditView(w http.ResponseWriter, r *http.Request) {
	x, err := s.Queries.GetPaymentAudit(r.Context(), httpx.PathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	paid := ""
	if x.PaidAt.Valid {
		paid = s.TS(x.PaidAt.Int64)
	}
	rows := [][2]string{{"Transaction ID", x.Ref}, {"Gateway Ref", x.GatewayRef}, {"Username", x.Username}, {"Package Name", x.PlanName},
		{"Package Price", view.Money(x.Amount)}, {"Coupon Code", x.Coupon}, {"Channel", x.Channel}, {"Payment Link", x.PayUrl}, {"Status", x.Status},
		{"Created On", s.TS(x.CreatedAt)}, {"Expires On", s.TS(x.ExpiresAt)}, {"Paid Date", paid}}
	s.Render(w, r, 200, "pay_audit", core.Page{Title: "Payment Audit", Data: rows})
}
