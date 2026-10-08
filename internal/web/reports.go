package web

import (
	"encoding/csv"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
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
	Company, Query                 string
}

func parseDay(s string, loc *time.Location, def time.Time) time.Time {
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t
	}
	return def
}

// reportLoad runs the filter. Day bounds are local midnights in the app zone; "to" is inclusive.
func (s *Server) reportLoad(r *http.Request, period bool) (reportData, error) {
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
		Router: q.Get("router"), PlanID: q.Get("plan")}
	d.From, d.To = from.Format("2006-01-02"), to.Format("2006-01-02")
	planID, _ := strconv.ParseInt(d.PlanID, 10, 64)
	rows, err := s.queries.ReportTransactions(r.Context(), db.ReportTransactionsParams{
		FromTs: from.Unix(), ToTs: to.AddDate(0, 0, 1).Unix(), Type: d.Type, Method: d.Method, RouterName: d.Router, PlanID: planID})
	if err != nil {
		return d, err
	}
	d.Rows, d.Count = rows, len(rows)
	byType, byMethod := map[string]*repTotal{}, map[string]*repTotal{}
	add := func(m map[string]*repTotal, k string, p int64) {
		if m[k] == nil {
			m[k] = &repTotal{Name: k}
		}
		m[k].Count++
		m[k].Sum += p
	}
	for _, t := range rows {
		d.Sum += t.Price
		add(byType, t.Type, t.Price)
		add(byMethod, t.Method, t.Price)
	}
	flat := func(m map[string]*repTotal) (out []repTotal) {
		for _, v := range m {
			out = append(out, *v)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return
	}
	d.ByType, d.ByMethod = flat(byType), flat(byMethod)
	// the query string that reproduces this report, for the export and print links
	v := url.Values{"type": {d.Type}, "method": {d.Method}, "router": {d.Router}, "plan": {d.PlanID}}
	if period {
		v.Set("from", d.From)
		v.Set("to", d.To)
	} else {
		v.Set("date", d.From)
	}
	d.Query = v.Encode()
	if set, err := s.loadSettings(r.Context()); err == nil {
		d.Company = set["company_name"]
	}
	return d, nil
}

func isPeriod(r *http.Request) bool {
	return r.URL.Query().Has("from") || r.URL.Query().Has("to")
}

func (s *Server) reportPage(period bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := s.reportLoad(r, period)
		if err != nil {
			s.fail(w, "report", err)
			return
		}
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
	d, err := s.reportLoad(r, isPeriod(r))
	if err != nil {
		s.fail(w, "report print", err)
		return
	}
	s.render(w, r, 200, "report_print", Page{Title: "Reports", Data: d})
}

// csvSafe defuses spreadsheet formula injection.
func csvSafe(rec []string) {
	for i, f := range rec {
		if f != "" && strings.ContainsRune("=+-@", rune(f[0])) {
			rec[i] = "'" + f
		}
	}
}

func (s *Server) reportExport(w http.ResponseWriter, r *http.Request) {
	d, err := s.reportLoad(r, isPeriod(r))
	if err != nil {
		s.fail(w, "report export", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="report-`+d.From+`_`+d.To+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Invoice", "Username", "Plan Name", "Type", "Plan Price", "Method", "Routers", "Created On", "Expires On"})
	for _, t := range d.Rows {
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

func (s *Server) reportRoutes(mux *http.ServeMux, all func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/reports", all(s.reportPage(false)))
	mux.Handle("GET /admin/reports/period", all(s.reportPage(true)))
	mux.Handle("GET /admin/reports/print", all(http.HandlerFunc(s.reportPrint)))
	mux.Handle("GET /admin/reports/export", all(http.HandlerFunc(s.reportExport)))
	mux.Handle("GET /admin/transactions/{id}/invoice", all(http.HandlerFunc(s.trxInvoice)))
}
