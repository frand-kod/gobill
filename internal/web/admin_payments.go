package web

// Payment request audit list, export and detail.

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"strings"
)

func (s *Server) pgList(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "payment gateways", err)
		return
	}
	rows := []listRow{{1, []string{"Tripay", "Disabled"}}, {2, []string{"Duitku", "Disabled"}}}
	for i, g := range []string{"tripay", "duitku"} {
		if st["payment_gateway"] == g {
			rows[i].Cells[1] = "Enabled"
		}
	}
	lp := listPage{Heading: "Online payments", Base: "/admin/payment-gateway", Cols: []string{"Gateway", "Status"},
		Rows:  rows,
		Links: []option{{"/admin/settings/payment", "Settings"}, {"/admin/payment-gateway/audit", "Payment Audit"}}}
	s.renderList(w, r, lp)
}

var pgAuditCols = []string{"Transaction ID", "Gateway Ref", "Username", "Package Name", "Package Price", "Channel", "Status", "Created On", "Expires On", "Paid Date"}

func (s *Server) pgAuditParams(r *http.Request) db.SearchPaymentRequestsParams {
	_, _, f, t := s.dateRange(r)
	return db.SearchPaymentRequestsParams{Q: strings.TrimSpace(r.URL.Query().Get("q")), Status: r.URL.Query().Get("status"), FromTs: f, ToTs: t}
}

func (s *Server) pgAuditRows(rs []db.SearchPaymentRequestsRow) [][]string {
	out := [][]string{}
	for _, x := range rs {
		paid := ""
		if x.PaidAt.Valid {
			paid = s.ts(x.PaidAt.Int64)
		}
		out = append(out, []string{x.Ref, x.GatewayRef, x.Username, x.PlanName, money(x.Amount), x.Channel, x.Status, s.ts(x.CreatedAt), s.ts(x.ExpiresAt), paid})
	}
	return out
}

var pgStatuses = []option{{"", "Status"}, {"pending", "pending"}, {"paid", "paid"}, {"failed", "failed"}, {"expired", "expired"}}

func (s *Server) pgAudit(w http.ResponseWriter, r *http.Request) {
	q, pg := paging(r)
	p := s.pgAuditParams(r)
	p.PageLimit, p.PageOffset = pg.limit(), pg.offset()
	rs, err := s.queries.SearchPaymentRequests(r.Context(), p)
	if err != nil {
		s.fail(w, "payment audit", err)
		return
	}
	from, to, _, _ := s.dateRange(r)
	lp := listPage{Heading: "Payment Audit", Base: "/admin/payment-gateway/audit", Q: q, Searchable: true, Dates: true, From: from, To: to,
		Cols: pgAuditCols, ViewLink: true, Filters: []filter{{"status", p.Status, pgStatuses}}}
	for i, c := range s.pgAuditRows(rs) {
		lp.Rows = append(lp.Rows, listRow{rs[i].ID, c})
	}
	lp.Links = []option{{"/admin/payment-gateway/audit/export?" + lp.query().Encode(), "Export CSV"}}
	total, err := s.queries.CountPaymentRequests(r.Context(), db.CountPaymentRequestsParams{Q: p.Q, Status: p.Status, FromTs: p.FromTs, ToTs: p.ToTs})
	if err != nil {
		s.fail(w, "count payments", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) pgAuditExport(w http.ResponseWriter, r *http.Request) {
	p := s.pgAuditParams(r)
	p.PageLimit = -1
	rs, err := s.queries.SearchPaymentRequests(r.Context(), p)
	if err != nil {
		s.fail(w, "export payment audit", err)
		return
	}
	s.writeCSV(w, "payment-audit", pgAuditCols, s.pgAuditRows(rs))
}

func (s *Server) pgAuditView(w http.ResponseWriter, r *http.Request) {
	x, err := s.queries.GetPaymentAudit(r.Context(), pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	paid := ""
	if x.PaidAt.Valid {
		paid = s.ts(x.PaidAt.Int64)
	}
	rows := [][2]string{{"Transaction ID", x.Ref}, {"Gateway Ref", x.GatewayRef}, {"Username", x.Username}, {"Package Name", x.PlanName},
		{"Package Price", money(x.Amount)}, {"Coupon Code", x.Coupon}, {"Channel", x.Channel}, {"Payment Link", x.PayUrl}, {"Status", x.Status},
		{"Created On", s.ts(x.CreatedAt)}, {"Expires On", s.ts(x.ExpiresAt)}, {"Paid Date", paid}}
	s.render(w, r, 200, "pay_audit", Page{Title: "Payment Audit", Data: rows})
}
