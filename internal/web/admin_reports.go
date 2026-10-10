package web

// Income reports, print and export, invoices and transaction list.

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

type repTotal struct {
	Name  string
	Count int
	Sum   int64
}

type reportData struct {
	Period                         bool
	From, To, Type, Method, Router string
	PlanID                         string
	Types, Routers, Plans          []option
	Rows                           []db.Transaction
	Count                          int
	Sum                            int64
	ByType, ByMethod               []repTotal
	Company, Query, Base           string
	Prev, Next                     int  // row table page numbers, 0 = none
	Truncated                      bool // print: rows capped at reportPrintCap
}

// reportPrintCap bounds the print view; the CSV export still has every row.
const reportPrintCap = 5000

func parseDay(s string, loc *time.Location, def time.Time) time.Time {
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t
	}
	return def
}

// PageURL links another page of the row table, keeping the filter.
func (d reportData) PageURL(page int) string {
	return d.Base + "?" + d.Query + "&page=" + strconv.Itoa(page)
}

// reportFilter reads the request filter. Day bounds are local midnights in the app zone; "to" is inclusive.
func (s *Server) reportFilter(r *http.Request, period bool) (reportData, db.ReportTransactionsParams) {
	q := r.URL.Query()
	loc := s.location()
	today := time.Now().In(loc)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	from, to := parseDay(q.Get("date"), loc, today), time.Time{}
	if period {
		from = parseDay(q.Get("from"), loc, today.AddDate(0, 0, -30))
		to = parseDay(q.Get("to"), loc, today)
	} else {
		to = from
	}
	d := reportData{Period: period, Type: q.Get("type"), Method: strings.TrimSpace(q.Get("method")),
		Router: q.Get("router"), PlanID: q.Get("plan"), Base: "/admin/reports"}
	d.From, d.To = from.Format("2006-01-02"), to.Format("2006-01-02")
	planID, _ := strconv.ParseInt(d.PlanID, 10, 64)
	// the query string that reproduces this report, for the export, print and page links
	v := url.Values{"type": {d.Type}, "method": {d.Method}, "router": {d.Router}, "plan": {d.PlanID}}
	if period {
		v.Set("from", d.From)
		v.Set("to", d.To)
		d.Base = "/admin/reports/period"
	} else {
		v.Set("date", d.From)
	}
	d.Query = v.Encode()
	return d, db.ReportTransactionsParams{FromTs: from.Unix(), ToTs: to.AddDate(0, 0, 1).Unix(), Type: d.Type, Method: d.Method, RouterName: d.Router, PlanID: planID}
}

// reportLoad fills the totals (count, sum, by type, by method) with SQL aggregates. Rows are left to the caller.
func (s *Server) reportLoad(r *http.Request, period bool) (reportData, db.ReportTransactionsParams, error) {
	d, a := s.reportFilter(r, period)
	ctx := r.Context()
	tot, err := s.queries.ReportTotals(ctx, db.ReportTotalsParams(a))
	if err != nil {
		return d, a, err
	}
	d.Count, d.Sum = int(tot.Cnt), tot.Total
	bt, err := s.queries.ReportTotalsByType(ctx, db.ReportTotalsByTypeParams(a))
	if err != nil {
		return d, a, err
	}
	for _, x := range bt {
		d.ByType = append(d.ByType, repTotal{Name: x.Name, Count: int(x.Cnt), Sum: x.Total})
	}
	bm, err := s.queries.ReportTotalsByMethod(ctx, db.ReportTotalsByMethodParams(a))
	if err != nil {
		return d, a, err
	}
	for _, x := range bm {
		d.ByMethod = append(d.ByMethod, repTotal{Name: x.Name, Count: int(x.Cnt), Sum: x.Total})
	}
	if set, err := s.loadSettings(ctx); err == nil {
		d.Company = set["company_name"]
	}
	return d, a, nil
}

// reportRows reads rows of the filtered report in date order.
func (s *Server) reportRows(ctx context.Context, a db.ReportTransactionsParams, limit, offset int64) ([]db.Transaction, error) {
	return s.queries.ReportTransactionsPage(ctx, db.ReportTransactionsPageParams{FromTs: a.FromTs, ToTs: a.ToTs, Type: a.Type,
		Method: a.Method, RouterName: a.RouterName, PlanID: a.PlanID, PageLimit: limit, PageOffset: offset})
}

func isPeriod(r *http.Request) bool {
	return r.URL.Query().Has("from") || r.URL.Query().Has("to")
}

func (s *Server) reportPage(period bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, a, err := s.reportLoad(r, period)
		if err != nil {
			s.fail(w, "report", err)
			return
		}
		_, page, limit, off := paging(r)
		rows, err := s.reportRows(r.Context(), a, limit, off)
		if err != nil {
			s.fail(w, "report rows", err)
			return
		}
		if len(rows) > perPage {
			rows = rows[:perPage]
			d.Next = page + 1
		}
		if page > 1 {
			d.Prev = page - 1
		}
		d.Rows = rows
		d.Types = anyOpts("Type", "Hotspot", "PPPoE", "Balance")
		d.Routers = anyOpts("Routers")
		rs, err := s.queries.ListRouters(r.Context(), db.ListRoutersParams{Limit: 1000})
		if err != nil {
			s.fail(w, "report routers", err)
			return
		}
		for _, x := range rs {
			d.Routers = append(d.Routers, option{x.Name, x.Name})
		}
		opts, _, err := s.planOptions(r, false)
		if err != nil {
			s.fail(w, "report plans", err)
			return
		}
		d.Plans = append([]option{{"", "Plan"}}, opts...)
		title := "Daily Report"
		if period {
			title = "Period Report"
		}
		s.render(w, r, 200, "report", Page{Title: title, Data: d})
	}
}

func (s *Server) reportPrint(w http.ResponseWriter, r *http.Request) {
	d, a, err := s.reportLoad(r, isPeriod(r))
	if err != nil {
		s.fail(w, "report print", err)
		return
	}
	rows, err := s.reportRows(r.Context(), a, reportPrintCap+1, 0)
	if err != nil {
		s.fail(w, "report print rows", err)
		return
	}
	if len(rows) > reportPrintCap {
		rows = rows[:reportPrintCap]
		d.Truncated = true
	}
	d.Rows = rows
	s.render(w, r, 200, "report_print", Page{Title: "Reports", Data: d})
}

// csvSafe defuses spreadsheet formula injection.
func csvSafe(rec []string) {
	for i, f := range rec {
		if f != "" && strings.ContainsRune("=+-@\t\r", rune(f[0])) {
			rec[i] = "'" + f
		}
	}
}

func (s *Server) reportExport(w http.ResponseWriter, r *http.Request) {
	d, a := s.reportFilter(r, isPeriod(r))
	rows, err := s.queries.ReportTransactions(r.Context(), db.ReportTransactionsParams(a))
	if err != nil {
		s.fail(w, "report export", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="report-`+d.From+`_`+d.To+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Invoice", "Username", "Plan Name", "Type", "Plan Price", "Method", "Routers", "Created On", "Expires On"})
	for _, t := range rows {
		rec := []string{t.Invoice, t.Username, t.PlanName, t.Type, strconv.FormatInt(t.Price, 10), t.Method, t.RouterName, s.ts(t.CreatedAt), s.ts(t.PeriodEnd)}
		csvSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}

type invoiceData struct {
	T                        db.Transaction
	Customer                 db.Customer
	Company, Address, Footer string
}

// invoice renders one transaction as a printable invoice (admin and portal share it).
func (s *Server) invoice(w http.ResponseWriter, r *http.Request, t db.Transaction) {
	c := db.Customer{Username: t.Username, Fullname: t.Username} // deleted customer: snapshot only
	if t.CustomerID.Valid {
		var err error
		if c, err = s.queries.GetCustomer(r.Context(), t.CustomerID.Int64); err != nil {
			s.fail(w, "invoice customer", err)
			return
		}
	}
	set, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "invoice settings", err)
		return
	}
	s.render(w, r, 200, "invoice", Page{Title: "Invoice", Data: invoiceData{t, c, set["company_name"], set["address"], set["note"]}})
}

func (s *Server) trxInvoice(w http.ResponseWriter, r *http.Request) {
	t, err := s.queries.GetTransaction(r.Context(), pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.invoice(w, r, t)
}

func (s *Server) trxList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	sort, dir, param := listSort(r, "date", "amount", "username")
	// ?customer=ID limits the list to one customer (the customer detail page links here)
	cid, _ := strconv.ParseInt(r.URL.Query().Get("customer"), 10, 64)
	rows, err := s.queries.SearchTransactions(r.Context(), db.SearchTransactionsParams{Q: q, Sort: param, CustomerID: cid, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list transactions", err)
		return
	}
	lp := listPage{Heading: "Transactions", Base: "/admin/transactions", Q: q, Searchable: true, InvoiceLink: true,
		Cols:     []string{"Invoice", "Date", "Username", "Plan Name", "Type", "Method", "Plan Price"},
		SortKeys: []string{"", "date", "username", "", "", "", "amount"}, Sort: sort, Dir: dir}
	if cid > 0 {
		label := strconv.FormatInt(cid, 10)
		if c, err := s.queries.GetCustomer(r.Context(), cid); err == nil {
			label = c.Username
		}
		lp.Filters = []filter{{"customer", strconv.FormatInt(cid, 10), []option{{strconv.FormatInt(cid, 10), label}}}}
	}
	for _, t := range rows {
		lp.Rows = append(lp.Rows, listRow{t.ID, []string{t.Invoice, s.ts(t.CreatedAt), t.Username, t.PlanName, t.Type, t.Method, money(t.Price)}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}
