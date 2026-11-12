package web

// Voucher generation, list, print, view and redeem.

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/skip2/go-qrcode"
)

const (
	maxVouchers = 500
	// no 0/O/1/I/l: codes get typed from paper
	upperChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	lowerChars = "abcdefghjkmnpqrstuvwxyz23456789"
	mixedChars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
	digitChars = "0123456789"
)

var (
	vchStatuses = []option{{Value: "", Label: "All"}, {Value: "unused", Label: "Unused"}, {Value: "used", Label: "Used"}}
	prefixRe    = regexp.MustCompile(`^[A-Za-z0-9\-_.,]*$`) // old PHP alphanumeric(x, "-_.,")
)

// Minimum generated code lengths (brute-force resistance); shorter existing codes still redeem.
const (
	minVoucherLen        = 8
	minVoucherLenNumbers = 10
)

// randomCode draws n characters from charset using crypto/rand (rejection sampling, no modulo bias).
func randomCode(charset string, n int) (string, error) {
	out := make([]byte, 0, n)
	limit := 256 - 256%len(charset)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, charset[int(b)%len(charset)])
			}
		}
	}
	return string(out), nil
}

func (s *Server) planOptions(r *http.Request, enabledOnly bool) ([]option, map[int64]db.Plan, error) {
	plans, err := s.Queries.ListPlans(r.Context(), db.ListPlansParams{Limit: 1000})
	if err != nil {
		return nil, nil, err
	}
	var opts []option
	byID := map[int64]db.Plan{}
	isoPlan, _ := s.IsolirIDs(r.Context())
	for _, p := range plans {
		byID[p.ID] = p // rows and names still resolve; only the options leave the isolir plan out
		if p.ID != isoPlan && (!enabledOnly || p.Enabled == 1) {
			opts = append(opts, option{Value: fmt.Sprint(p.ID), Label: fmt.Sprintf("%s (%s)", p.Name, view.Money(p.Price))})
		}
	}
	return opts, byID, nil
}

// voucherViewer is the admin whose vouchers a role may see: 0 = all. Agent and Sales see the vouchers they
// generated, those of their own Sales, and (Agent) the codes of their consignment batches. Report is not limited.
func voucherViewer(a *db.Admin) int64 {
	if httpx.OneOf(a.Role, "Agent", "Sales") {
		return a.ID
	}
	return 0
}

// maskCode hides all but the first 2 characters of a voucher code.
func maskCode(c string) string {
	if len(c) <= 2 {
		return "****"
	}
	return c[:2] + "****"
}

func (s *Server) vchList(w http.ResponseWriter, r *http.Request) {
	q, pg := view.Paging(r)
	status := r.URL.Query().Get("status")
	if status != "unused" && status != "used" {
		status = ""
	}
	role := adminFrom(r).Role
	canSee := httpx.OneOf(role, "SuperAdmin", "Admin", "Agent", "Sales")
	// A masked role must not find an unused code by its full text (an unused code is free service), so its
	// search matches used codes only. "none" matches no row: unused codes are listed only without a search.
	qStatus := status
	if !canSee && q != "" {
		qStatus = "used"
		if status == "unused" {
			qStatus = "none"
		}
	}
	rows, err := s.Queries.SearchVouchers(r.Context(), db.SearchVouchersParams{Q: q, Status: qStatus, Viewer: voucherViewer(adminFrom(r)), PageLimit: pg.Limit(), PageOffset: pg.Offset()})
	if err != nil {
		s.Fail(w, "list vouchers", err)
		return
	}
	_, plans, err := s.planOptions(r, false)
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	lp := listPage{Heading: "Voucher", Base: "/admin/vouchers", Q: q, Searchable: true, DeleteOnly: true,
		CanCreate: httpx.OneOf(role, "SuperAdmin", "Admin", "Agent", "Sales"), CanEdit: httpx.OneOf(role, "SuperAdmin", "Admin"),
		Filters: []filter{{Name: "status", Val: status, Opts: vchStatuses, Label: "Status"}},
		Cols:    []string{"Code Voucher", "Plan Name", "Status", "Created", "Used"}}
	if lp.CanEdit {
		lp.Bulk = []bulkAction{{Path: "delete-many", Label: "Delete selected", Danger: true, All: false, Page: false}, {Path: "remove-old", Label: "Delete used vouchers older than 3 months", Danger: true, All: true, Page: false}}
	}
	if lp.CanCreate {
		lp.Links = []option{{Value: "/admin/vouchers/redeem", Label: "Redeem Voucher"}, {Value: "/admin/vouchers/print?limit=36", Label: "Print"}}
	}
	for _, v := range rows {
		used := ""
		if v.UsedAt.Valid {
			used = s.TS(v.UsedAt.Int64)
		}
		code := v.Code
		if v.Status == "unused" && !canSee { // an unused code is free service
			code = maskCode(code)
		}
		lp.Rows = append(lp.Rows, listRow{ID: v.ID, Cells: []string{code, plans[v.PlanID].Name, v.Status, s.TS(v.CreatedAt), used}})
	}
	total, err := s.Queries.CountVouchers(r.Context(), db.CountVouchersParams{Q: q, Status: qStatus, Viewer: voucherViewer(adminFrom(r))})
	if err != nil {
		s.Fail(w, "count vouchers", err)
		return
	}
	lp.Finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) vchFields(r *http.Request, v, e map[string]string) ([]field, error) {
	opts, _, err := s.planOptions(r, true)
	if err != nil {
		return nil, err
	}
	plan := text("plan", "Service Plan", v, e).As("select")
	plan.Options = opts
	format := text("voucher_format", "Voucher Format", v, e).As("select")
	format.Options = []option{{Value: "up", Label: "UPPERCASE"}, {Value: "low", Label: "lowercase"}, {Value: "rand", Label: "Random case"}, {Value: "numbers", Label: "Numbers"}}
	pn := text("print_now", "Print Now", v, e).As("checkbox")
	pn.Checked = v["print_now"] == "1"
	out := section([]field{plan, text("numbervoucher", "Number of Vouchers", v, e).As("number").Req(), pn}, "Voucher", "")
	code := section([]field{format, text("prefix", "Prefix", v, e),
		text("lengthcode", "Length Code", v, e).As("number").Req().WithHint("8-32, numbers: 10-32")}, "Code", "")
	for i := range code {
		code[i].Fold = true // "Advanced options": the defaults are fine for most vouchers
	}
	return append(out, code...), nil
}

func (s *Server) vchForm(w http.ResponseWriter, r *http.Request, status int, v, e map[string]string) {
	fs, err := s.vchFields(r, v, e)
	if err != nil {
		s.Fail(w, "voucher form", err)
		return
	}
	s.renderForm(w, r, status, formPage{Heading: "Add Vouchers", Action: "/admin/vouchers", Cancel: "/admin/vouchers", Fields: fs})
}

func (s *Server) vchNew(w http.ResponseWriter, r *http.Request) {
	s.vchForm(w, r, 200, map[string]string{"numbervoucher": "10", "voucher_format": "up", "lengthcode": "8", "print_now": "1"}, nil)
}

func (s *Server) vchGenerate(w http.ResponseWriter, r *http.Request) {
	v := httpx.FormVals(r, "plan", "numbervoucher", "voucher_format", "prefix", "lengthcode", "print_now")
	e := map[string]string{}
	planID, ok := httpx.PosInt(v["plan"])
	if _, err := s.Queries.GetPlan(r.Context(), planID); !ok || err != nil {
		e["plan"] = "This field is required"
	}
	if isoPlan, _ := s.IsolirIDs(r.Context()); ok && planID == isoPlan {
		e["plan"] = billing.ErrIsolirPlan.Error()
	}
	count, ok := httpx.PosInt(v["numbervoucher"])
	if !ok || count > maxVouchers {
		e["numbervoucher"] = fmt.Sprintf(s.T("Enter a number between 1 and %d"), maxVouchers)
	}
	charset := map[string]string{"up": upperChars, "low": lowerChars, "rand": mixedChars, "numbers": digitChars}[v["voucher_format"]]
	if charset == "" {
		e["voucher_format"] = "Invalid value"
	}
	minLen := int64(minVoucherLen)
	if v["voucher_format"] == "numbers" {
		minLen = minVoucherLenNumbers
	}
	length, ok := httpx.PosInt(v["lengthcode"])
	if !ok || length < minLen || length > 32 {
		e["lengthcode"] = fmt.Sprintf(s.T("Enter a number between %d and 32"), minLen)
	}
	if !prefixRe.MatchString(v["prefix"]) || len(v["prefix"]) > 16 {
		e["prefix"] = "Invalid value"
	}
	if len(e) > 0 {
		s.vchForm(w, r, http.StatusUnprocessableEntity, v, e)
		return
	}
	for n := int64(0); n < count; n++ {
		if err := s.newVoucher(r, planID, v["prefix"], charset, int(length)); err != nil {
			s.Fail(w, "create voucher", err)
			return
		}
	}
	back := fmt.Sprintf("/admin/vouchers/view?plan=%d&limit=%d", planID, count)
	if v["print_now"] == "1" {
		back = fmt.Sprintf("/admin/vouchers/print?plan=%d&limit=%d", planID, count)
	}
	s.done(w, r, back, "Create Vouchers Successfully", "voucher.generate", fmt.Sprintf("%d x plan %d", count, planID))
}

// newVoucher inserts one voucher, redrawing the code on the (unlikely) UNIQUE clash.
func (s *Server) newVoucher(r *http.Request, planID int64, prefix, charset string, length int) error {
	for try := 0; try < 5; try++ {
		code, err := randomCode(charset, length)
		if err != nil {
			return err
		}
		_, err = s.Queries.CreateVoucher(r.Context(), db.CreateVoucherParams{Code: prefix + code, PlanID: planID,
			GeneratedBy: sql.NullInt64{Int64: adminFrom(r).ID, Valid: true}})
		if !httpx.IsUnique(err) {
			return err
		}
	}
	return errors.New("could not find a free voucher code")
}

func (s *Server) vchDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if v, err := s.Queries.GetVoucher(r.Context(), httpx.PathID(r)); err == nil {
		name = v.Code
	}
	s.remove(w, r, "/admin/vouchers", "voucher", "Voucher is in use", name, func(id int64) error {
		return s.Queries.DeleteVoucher(r.Context(), id)
	})
}

// vchDeleteMany deletes the selected vouchers, used ones too (old PHP voucher-delete-many).
func (s *Server) vchDeleteMany(w http.ResponseWriter, r *http.Request) {
	s.bulkDelete(w, r, "/admin/vouchers", "voucher", "Voucher is in use", func(q *db.Queries, ids []int64) (int64, error) {
		return q.DeleteVouchersByIDs(r.Context(), ids)
	})
}

// vchRemoveOld deletes used vouchers older than 3 months (old PHP remove-voucher); ids are ignored.
func (s *Server) vchRemoveOld(w http.ResponseWriter, r *http.Request) {
	n, err := s.Queries.DeleteOldUsedVouchers(r.Context(), sql.NullInt64{Int64: time.Now().AddDate(0, -3, 0).Unix(), Valid: true})
	if err != nil {
		s.Fail(w, "remove old vouchers", err)
		return
	}
	s.bulkDone(w, r, "/admin/vouchers", "voucher.remove_old", n)
}

type printItem struct {
	Code, Plan, Price string
	QR                template.URL
}

type printPage struct {
	Company string
	Layout  string // a4, 58 or 80
	Layouts []printLayout
	Items   []printItem
}

type printLayout struct {
	Name, Href string
	On         bool
}

// vchPrint lays out the newest unused vouchers (optionally of one plan) for printing.
func (s *Server) vchPrint(w http.ResponseWriter, r *http.Request) {
	planID, _ := strconv.ParseInt(r.URL.Query().Get("plan"), 10, 64)
	limit, ok := httpx.PosInt(r.URL.Query().Get("limit"))
	if !ok || limit > maxVouchers {
		limit = 36
	}
	rows, err := s.Queries.SearchVouchers(r.Context(), db.SearchVouchersParams{Status: "unused", PlanID: planID, Viewer: voucherViewer(adminFrom(r)), PageLimit: limit})
	if err != nil {
		s.Fail(w, "print vouchers", err)
		return
	}
	_, plans, err := s.planOptions(r, false)
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	settings, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "settings", err)
		return
	}
	layout := "a4"
	if l := r.URL.Query().Get("layout"); l == "58" || l == "80" {
		layout = l
	}
	q := r.URL.Query()
	pp := printPage{Company: settings["company_name"], Layout: layout}
	for _, o := range []struct{ key, name string }{{"a4", "A4"}, {"58", "Thermal 58 mm"}, {"80", "Thermal 80 mm"}} {
		q.Set("layout", o.key)
		pp.Layouts = append(pp.Layouts, printLayout{o.name, "?" + q.Encode(), o.key == layout})
	}
	for _, v := range rows {
		png, err := qrcode.Encode(v.Code, qrcode.Medium, 160)
		if err != nil {
			slog.Error("qr", "err", err)
			continue
		}
		p := plans[v.PlanID]
		pp.Items = append(pp.Items, printItem{v.Code, p.Name, view.Money(p.Price),
			template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))})
	}
	s.Render(w, r, 200, "print", Page{Title: "Voucher", Data: pp})
}

// vchView shows the newest unused codes of a plan as plain text after generating (old voucher/view).
func (s *Server) vchView(w http.ResponseWriter, r *http.Request) {
	planID, _ := strconv.ParseInt(r.URL.Query().Get("plan"), 10, 64)
	limit, ok := httpx.PosInt(r.URL.Query().Get("limit"))
	if !ok || limit > maxVouchers {
		limit = 36
	}
	plan, err := s.Queries.GetPlan(r.Context(), planID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rows, err := s.Queries.SearchVouchers(r.Context(), db.SearchVouchersParams{Status: "unused", PlanID: planID, Viewer: voucherViewer(adminFrom(r)), PageLimit: limit})
	if err != nil {
		s.Fail(w, "view vouchers", err)
		return
	}
	codes := make([]string, len(rows))
	for i, v := range rows {
		codes[i] = v.Code
	}
	s.Render(w, r, 200, "voucher_view", Page{Title: "Voucher", Flash: s.Sessions.PopString(r.Context(), "flash"), Data: struct {
		Plan, Codes, PrintURL string
		Count                 int
	}{plan.Name, strings.Join(codes, "\n"), fmt.Sprintf("/admin/vouchers/print?plan=%d&limit=%d", planID, limit), len(codes)}})
}

func (s *Server) vchRedeemForm(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"customer": r.URL.Query().Get("customer")}
	s.renderForm(w, r, 200, redeemPage(v, nil))
}

func redeemPage(v, e map[string]string) formPage {
	return formPage{Heading: "Redeem Voucher", Action: "/admin/vouchers/redeem", Cancel: "/admin/vouchers",
		Fields: []field{customerPick("customer", "Username", v, e).Req(), text("code", "Code Voucher", v, e).Req()}}
}

func (s *Server) vchRedeem(w http.ResponseWriter, r *http.Request) {
	v := httpx.FormVals(r, "customer", "code")
	e := map[string]string{}
	c, err := s.Queries.GetCustomerByUsername(r.Context(), v["customer"])
	if err == sql.ErrNoRows {
		e["customer"] = "Customer not found"
	} else if err != nil {
		s.Fail(w, "get customer", err)
		return
	}
	if v["code"] == "" {
		e["code"] = "This field is required"
	}
	if len(e) == 0 && voucherViewer(adminFrom(r)) != 0 {
		// a code outside the admin's own vouchers answers like a code that does not exist
		if _, err := s.Queries.VoucherVisibleByCode(r.Context(), db.VoucherVisibleByCodeParams{Code: v["code"], Viewer: voucherViewer(adminFrom(r))}); err != nil {
			e["code"] = "Voucher not valid or already used"
		}
	}
	if len(e) == 0 {
		if s.Billing == nil {
			s.Fail(w, "redeem voucher", errors.New("billing service not configured"))
			return
		}
		switch err := s.Billing.RedeemVoucher(r.Context(), v["code"], c.ID); {
		case errors.Is(err, billing.ErrVoucherInvalid):
			e["code"] = "Voucher not valid or already used"
		case errors.Is(err, billing.ErrInactive):
			e["code"] = "account is not active"
		case err != nil:
			s.Fail(w, "redeem voucher", err)
			return
		default:
			s.done(w, r, fmt.Sprint("/admin/customers/", c.ID), "Voucher redeemed", "voucher.redeem", v["code"]+" -> "+c.Username)
			return
		}
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, redeemPage(v, e))
}
