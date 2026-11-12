package web

// Income reports, print and export, invoices and transaction list.

import (
	"context"
	"encoding/csv"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

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
	Count, RowCount                int // RowCount: transaction rows (view.Pager); Count adds the agent deposits
	Sum                            int64
	ByType, ByMethod               []repTotal
	Company, Base                  string
	Methods                        []option // method filter: distinct income methods
	CanInvoice                     bool     // viewer may open invoices (the Report role may not)
	// print header and footer
	Address, PrintedAt, PrintedBy, FromDMY, ToDMY, PlanName string
	Avg                                                     int64
	// Query reproduces this report for the export, print and page links. template.URL: it is url.Values.Encode
	// output, and html/template would otherwise escape its = and & so the links lose the filter.
	Query     template.URL
	Nav       view.PageNav // row table view.Pager
	Truncated bool         // print: rows capped at reportPrintCap
	// Usage section of the Period Report, managers only (like the dashboard Usage card)
	ShowUsage, UsageErr bool
	Usage               usageWin
	UsageQuery          template.URL // from and to for the usage CSV link
}

// reportUsageTop is how many heaviest users the Period Report lists.
const reportUsageTop = 20

const agentDepositMethod = "Agent - Deposit"

// wantsDeposits: deposits have no type, router or plan, so those filters hide them.
func wantsDeposits(d reportData, a db.ReportTransactionsParams) bool {
	return d.Type == "" && d.Router == "" && a.PlanID == 0 && (d.Method == "" || d.Method == agentDepositMethod)
}

// reportPrintCap bounds the print view; the CSV export still has every row.
const reportPrintCap = 5000

func parseDay(s string, loc *time.Location, def time.Time) time.Time {
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t
	}
	return def
}

// reportFilter reads the request filter. Day bounds are local midnights in the app zone; "to" is inclusive.
func (s *Server) reportFilter(r *http.Request, period bool) (reportData, db.ReportTransactionsParams) {
	q := r.URL.Query()
	loc := s.Location()
	now := s.now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from, to := parseDay(q.Get("date"), loc, today), time.Time{}
	if period {
		start, _ := s.cycleStart(r.Context(), now) // the dashboard's income period, so the numbers match
		from = parseDay(q.Get("from"), loc, start)
		to = parseDay(q.Get("to"), loc, today)
		if to.Before(from) {
			from, to = to, from
		}
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
	d.Query = template.URL(v.Encode())
	d.UsageQuery = template.URL(url.Values{"from": {d.From}, "to": {d.To}}.Encode())
	return d, db.ReportTransactionsParams{FromTs: from.Unix(), ToTs: to.AddDate(0, 0, 1).Unix(), Type: d.Type, Method: d.Method, RouterName: d.Router, PlanID: planID}
}

// reportLoad fills the totals (count, sum, by type, by method) with SQL aggregates. Rows are left to the caller.
func (s *Server) reportLoad(r *http.Request, period bool) (reportData, db.ReportTransactionsParams, error) {
	d, a := s.reportFilter(r, period)
	ctx := r.Context()
	tot, err := s.Queries.ReportTotals(ctx, db.ReportTotalsParams(a))
	if err != nil {
		return d, a, err
	}
	d.Count, d.RowCount, d.Sum = int(tot.Cnt), int(tot.Cnt), tot.Total
	bt, err := s.Queries.ReportTotalsByType(ctx, db.ReportTotalsByTypeParams(a))
	if err != nil {
		return d, a, err
	}
	for _, x := range bt {
		d.ByType = append(d.ByType, repTotal{Name: x.Name, Count: int(x.Cnt), Sum: x.Total})
	}
	bm, err := s.Queries.ReportTotalsByMethod(ctx, db.ReportTotalsByMethodParams(a))
	if err != nil {
		return d, a, err
	}
	for _, x := range bm {
		d.ByMethod = append(d.ByMethod, repTotal{Name: x.Name, Count: int(x.Cnt), Sum: x.Total})
	}
	// Agent consignment deposits are income of their own (the codes they sold are excluded from the rows).
	// They have no type, router or plan, so those filters hide them.
	if wantsDeposits(d, a) {
		ap, err := s.Queries.ReportAgentPayments(ctx, db.ReportAgentPaymentsParams{FromTs: a.FromTs, ToTs: a.ToTs})
		if err != nil {
			return d, a, err
		}
		if ap.Cnt > 0 {
			x := repTotal{Name: agentDepositMethod, Count: int(ap.Cnt), Sum: ap.Total}
			d.ByType, d.ByMethod = append(d.ByType, x), append(d.ByMethod, x)
			d.Count, d.Sum = d.Count+x.Count, d.Sum+x.Sum
		}
	}
	if d.Count > 0 {
		d.Avg = d.Sum / int64(d.Count)
	}
	if set, err := s.LoadSettings(ctx); err == nil {
		d.Company, d.Address = set["company_name"], set["address"]
	}
	d.CanInvoice = s.canOpenInvoices(ctx)
	return d, a, nil
}

// reportMethodOpts fills the method filter: the methods seen in income, the deposit pseudo-method, and the
// requested one even when it matches nothing (old ?method= links keep working).
func (s *Server) reportMethodOpts(ctx context.Context, d *reportData) error {
	ms, err := s.Queries.ReportMethods(ctx)
	if err != nil {
		return err
	}
	ms = append(ms, agentDepositMethod)
	if d.Method != "" && !slices.Contains(ms, d.Method) {
		ms = append(ms, d.Method)
	}
	d.Methods = anyOpts("Method", ms...)
	return nil
}

// reportRows reads rows of the filtered report in date order.
func (s *Server) reportRows(ctx context.Context, a db.ReportTransactionsParams, limit, offset int64) ([]db.Transaction, error) {
	rows, err := s.Queries.ReportTransactionsPage(ctx, db.ReportTransactionsPageParams{FromTs: a.FromTs, ToTs: a.ToTs, Type: a.Type,
		Method: a.Method, RouterName: a.RouterName, PlanID: a.PlanID, PageLimit: limit, PageOffset: offset})
	out := make([]db.Transaction, len(rows)) // the view's row type has the same fields
	for i, x := range rows {
		out[i] = db.Transaction(x)
	}
	return out, err
}

// maxUsageDays caps the usage range: one bar per day. ponytail: a longer range shows its last 366 days;
// aggregate by week or month if anyone needs years.
const maxUsageDays = 366

// usageBounds is the first and last day of the usage section for the report range (d.From, d.To).
// ok is false when the range is backwards.
func (s *Server) usageBounds(d reportData) (from, to time.Time, ok bool) {
	loc := s.Location()
	from, to = parseDay(d.From, loc, time.Time{}), parseDay(d.To, loc, time.Time{})
	if to.Before(from) {
		return from, to, false
	}
	if first := to.AddDate(0, 0, -(maxUsageDays - 1)); from.Before(first) {
		from = first
	}
	return from, to, true
}

func isPeriod(r *http.Request) bool {
	return r.URL.Query().Has("from") || r.URL.Query().Has("to")
}

func (s *Server) reportPage(period bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, a, err := s.reportLoad(r, period)
		if err != nil {
			s.Fail(w, "report", err)
			return
		}
		_, pg := view.Paging(r)
		rows, err := s.reportRows(r.Context(), a, pg.Limit(), pg.Offset())
		if err != nil {
			s.Fail(w, "report rows", err)
			return
		}
		more := len(rows) > pg.Per
		if more {
			rows = rows[:pg.Per]
		}
		// d.Count is the number of rows the filter matches (the totals are computed over all of them)
		d.Nav = view.NewPageNav(pg, int64(d.RowCount), len(rows), more, func(page, per int) string {
			href := d.Base + "?" + string(d.Query) + "&page=" + strconv.Itoa(page)
			if per != view.PerPage {
				href += "&per=" + strconv.Itoa(per)
			}
			return href
		})
		d.Rows = rows
		d.Types = anyOpts("Type", "Hotspot", "PPPoE", "Balance")
		d.Routers = append(anyOpts("Routers"), option{"-radius", "RADIUS (no router)"}, option{"balance", "Balance"})
		rs, err := s.Queries.ListRouters(r.Context(), db.ListRoutersParams{Limit: 1000})
		if err != nil {
			s.Fail(w, "report routers", err)
			return
		}
		for _, x := range rs {
			d.Routers = append(d.Routers, option{x.Name, x.Name})
		}
		opts, _, err := s.planOptions(r, false)
		if err != nil {
			s.Fail(w, "report plans", err)
			return
		}
		if err := s.reportMethodOpts(r.Context(), &d); err != nil {
			s.Fail(w, "report methods", err)
			return
		}
		d.Plans = append([]option{{"", "Plan"}}, opts...)
		title := "Daily Report"
		if period {
			title = "Period Report"
			// RADIUS usage over the range, managers only like the dashboard Usage card. The router, plan,
			// type and method filters do not apply: radius_usage_daily has no such columns.
			if d.ShowUsage = ctxIsManager(r.Context()); d.ShowUsage {
				if from, to, ok := s.usageBounds(d); !ok {
					d.Usage = usageWin{Empty: true}
				} else if d.Usage, err = s.usageRange(r.Context(), from, to, s.Location(), reportUsageTop); err != nil {
					slog.Error("report usage", "err", err)
					d.UsageErr = true
				}
			}
		}
		s.Render(w, r, 200, "report", Page{Title: title, Data: d})
	}
}

func (s *Server) reportPrint(w http.ResponseWriter, r *http.Request) {
	d, a, err := s.reportLoad(r, isPeriod(r))
	if err != nil {
		s.Fail(w, "report print", err)
		return
	}
	rows, err := s.reportRows(r.Context(), a, reportPrintCap+1, 0)
	if err != nil {
		s.Fail(w, "report print rows", err)
		return
	}
	if len(rows) > reportPrintCap {
		rows = rows[:reportPrintCap]
		d.Truncated = true
	}
	d.Rows = rows
	loc := s.Location()
	now := s.now().In(loc)
	d.PrintedAt = now.Format("02/01/2006 15:04")
	if a := adminFrom(r); a != nil {
		d.PrintedBy = a.Fullname
		if d.PrintedBy == "" {
			d.PrintedBy = a.Username
		}
	}
	d.FromDMY, d.ToDMY = parseDay(d.From, loc, now).Format("02/01/2006"), parseDay(d.To, loc, now).Format("02/01/2006")
	if id, _ := strconv.ParseInt(d.PlanID, 10, 64); id > 0 {
		if p, err := s.Queries.GetPlan(r.Context(), id); err == nil {
			d.PlanName = p.Name
		}
	}
	title := "Daily Report"
	if d.Period {
		title = "Period Report"
	}
	s.Render(w, r, 200, "report_print", Page{Title: title, Data: d})
}

func (s *Server) reportExport(w http.ResponseWriter, r *http.Request) {
	d, a := s.reportFilter(r, isPeriod(r))
	rows, err := s.Queries.ReportTransactions(r.Context(), db.ReportTransactionsParams(a))
	if err != nil {
		s.Fail(w, "report export", err)
		return
	}
	var pays []db.ListAgentPaymentsRow
	if wantsDeposits(d, a) {
		if pays, err = s.Queries.ListAgentPayments(r.Context(), db.ListAgentPaymentsParams{FromTs: a.FromTs, ToTs: a.ToTs}); err != nil {
			s.Fail(w, "report export deposits", err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="report-`+d.From+`_`+d.To+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Invoice", "Username", "Plan Name", "Type", "Plan Price", "Method", "Routers", "Created On", "Expires On"})
	for _, t := range rows {
		rec := []string{t.Invoice, t.Username, t.PlanName, t.Type, strconv.FormatInt(t.Price, 10), t.Method, t.RouterName, s.TS(t.CreatedAt), s.TS(t.PeriodEnd)}
		httpx.CSVSafe(rec)
		cw.Write(rec)
	}
	for _, p := range pays { // agent deposits, so the file adds up to the report total
		rec := []string{"AGENT-" + strconv.FormatInt(p.ID, 10), p.AgentName, "", "", strconv.FormatInt(p.Amount, 10), agentDepositMethod, "", s.TS(p.CreatedAt), ""}
		httpx.CSVSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}

// usageExport is the usage CSV: one row per user per day in the Period Report range (radius_usage_daily).
// Managers only, like the dashboard Usage card.
func (s *Server) usageExport(w http.ResponseWriter, r *http.Request) {
	d, _ := s.reportFilter(r, true)
	from, to, ok := s.usageBounds(d)
	var rows []db.ListRadiusUsageDailyRangeRow
	if ok {
		var err error
		if rows, err = s.Queries.ListRadiusUsageDailyRange(r.Context(), db.ListRadiusUsageDailyRangeParams{Since: from.Format("2006-01-02"), Until: to.Format("2006-01-02")}); err != nil {
			s.Fail(w, "usage export", err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="usage-`+d.From+`_`+d.To+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Day", "Username", "Download bytes", "Upload bytes", "Total bytes"})
	for _, x := range rows {
		rec := []string{x.Day, x.Username, strconv.FormatInt(x.OutputOctets, 10), strconv.FormatInt(x.InputOctets, 10), strconv.FormatInt(x.InputOctets+x.OutputOctets, 10)}
		httpx.CSVSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}

func (s *Server) trxInvoice(w http.ResponseWriter, r *http.Request) {
	t, err := s.Queries.GetTransaction(r.Context(), httpx.PathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.Invoice(w, r, t)
}

func (s *Server) trxList(w http.ResponseWriter, r *http.Request) {
	q, pg := view.Paging(r)
	sort, dir, param := listSort(r, "date", "amount", "username")
	// ?customer=ID limits the list to one customer (the customer detail page links here)
	cid, _ := strconv.ParseInt(r.URL.Query().Get("customer"), 10, 64)
	rows, err := s.Queries.SearchTransactions(r.Context(), db.SearchTransactionsParams{Q: q, Sort: param, CustomerID: cid, PageLimit: pg.Limit(), PageOffset: pg.Offset()})
	if err != nil {
		s.Fail(w, "list transactions", err)
		return
	}
	lp := listPage{Heading: "Transactions", Base: "/admin/transactions", Q: q, Searchable: true, InvoiceLink: true,
		Cols:     []string{"Invoice", "Date", "Username", "Plan Name", "Type", "Method", "Plan Price"},
		SortKeys: []string{"", "date", "username", "", "", "", "amount"}, Sort: sort, Dir: dir}
	if cid > 0 {
		label := strconv.FormatInt(cid, 10)
		if c, err := s.Queries.GetCustomer(r.Context(), cid); err == nil {
			label = c.Username
		}
		lp.Filters = []filter{{"customer", strconv.FormatInt(cid, 10), []option{{strconv.FormatInt(cid, 10), label}}, "Customer"}}
	}
	for _, t := range rows {
		lp.Rows = append(lp.Rows, listRow{t.ID, []string{t.Invoice, s.TS(t.CreatedAt), t.Username, t.PlanName, t.Type, t.Method, view.Money(t.Price)}})
	}
	total, err := s.Queries.CountSearchTransactions(r.Context(), db.CountSearchTransactionsParams{Q: q, CustomerID: cid})
	if err != nil {
		s.Fail(w, "count transactions", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}
