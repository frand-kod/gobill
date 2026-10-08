package web

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/skip2/go-qrcode"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
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
	vchStatuses = []option{{"", "All"}, {"unused", "Unused"}, {"used", "Used"}}
	prefixRe    = regexp.MustCompile(`^[A-Za-z0-9\-_.,]*$`) // old PHP alphanumeric(x, "-_.,")
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
	plans, err := s.queries.ListPlans(r.Context(), db.ListPlansParams{Limit: 1000})
	if err != nil {
		return nil, nil, err
	}
	var opts []option
	byID := map[int64]db.Plan{}
	for _, p := range plans {
		byID[p.ID] = p
		if !enabledOnly || p.Enabled == 1 {
			opts = append(opts, option{fmt.Sprint(p.ID), fmt.Sprintf("%s (%s)", p.Name, money(p.Price))})
		}
	}
	return opts, byID, nil
}

func (s *Server) vchList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	status := r.URL.Query().Get("status")
	if status != "unused" && status != "used" {
		status = ""
	}
	rows, err := s.queries.SearchVouchers(r.Context(), db.SearchVouchersParams{Q: q, Status: status, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list vouchers", err)
		return
	}
	_, plans, err := s.planOptions(r, false)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	role := adminFrom(r).Role
	lp := listPage{Heading: "Voucher", Base: "/admin/vouchers", Q: q, Searchable: true, DeleteOnly: true,
		CanCreate: oneOf(role, "SuperAdmin", "Admin", "Agent", "Sales"), CanEdit: oneOf(role, "SuperAdmin", "Admin"),
		FilterName: "status", FilterVal: status, FilterOpts: vchStatuses,
		Cols: []string{"Code Voucher", "Plan Name", "Status", "Created", "Used"}}
	if lp.CanCreate {
		lp.Links = []option{{"/admin/vouchers/redeem", "Redeem Voucher"}, {"/admin/vouchers/print?limit=36", "Print"}}
	}
	for _, v := range rows {
		used := ""
		if v.UsedAt.Valid {
			used = s.ts(v.UsedAt.Int64)
		}
		lp.Rows = append(lp.Rows, listRow{v.ID, []string{v.Code, plans[v.PlanID].Name, v.Status, s.ts(v.CreatedAt), used}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) vchFields(r *http.Request, v, e map[string]string) ([]field, error) {
	opts, _, err := s.planOptions(r, true)
	if err != nil {
		return nil, err
	}
	plan := text("plan", "Service Plan", v, e).as("select")
	plan.Options = opts
	format := text("voucher_format", "Voucher Format", v, e).as("select")
	format.Options = []option{{"up", "UPPERCASE"}, {"low", "lowercase"}, {"rand", "Random case"}, {"numbers", "Numbers"}}
	pn := text("print_now", "Print Now", v, e).as("checkbox")
	pn.Checked = v["print_now"] == "1"
	out := section([]field{plan, text("numbervoucher", "Number of Vouchers", v, e).as("number").req()}, "Voucher", "")
	return append(out, section([]field{format, text("prefix", "Prefix", v, e),
		text("lengthcode", "Length Code", v, e).as("number").req().hint("4-32, numbers: 6-32"), pn}, "Code", "")...), nil
}

func (s *Server) vchForm(w http.ResponseWriter, r *http.Request, status int, v, e map[string]string) {
	fs, err := s.vchFields(r, v, e)
	if err != nil {
		s.fail(w, "voucher form", err)
		return
	}
	s.renderForm(w, r, status, formPage{"Add Vouchers", "/admin/vouchers", "/admin/vouchers", fs})
}

func (s *Server) vchNew(w http.ResponseWriter, r *http.Request) {
	s.vchForm(w, r, 200, map[string]string{"numbervoucher": "10", "voucher_format": "up", "lengthcode": "8"}, nil)
}

func (s *Server) vchGenerate(w http.ResponseWriter, r *http.Request) {
	v := formVals(r, "plan", "numbervoucher", "voucher_format", "prefix", "lengthcode", "print_now")
	e := map[string]string{}
	planID, ok := posInt(v["plan"])
	if _, err := s.queries.GetPlan(r.Context(), planID); !ok || err != nil {
		e["plan"] = "This field is required"
	}
	count, ok := posInt(v["numbervoucher"])
	if !ok || count > maxVouchers {
		e["numbervoucher"] = "Enter a number between 1 and " + strconv.Itoa(maxVouchers)
	}
	charset := map[string]string{"up": upperChars, "low": lowerChars, "rand": mixedChars, "numbers": digitChars}[v["voucher_format"]]
	if charset == "" {
		e["voucher_format"] = "Invalid value"
	}
	minLen := int64(4)
	if v["voucher_format"] == "numbers" {
		minLen = 6
	}
	length, ok := posInt(v["lengthcode"])
	if !ok || length < minLen || length > 32 {
		e["lengthcode"] = fmt.Sprintf("Enter a number between %d and 32", minLen)
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
			s.fail(w, "create voucher", err)
			return
		}
	}
	back := "/admin/vouchers"
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
		_, err = s.queries.CreateVoucher(r.Context(), db.CreateVoucherParams{Code: prefix + code, PlanID: planID,
			GeneratedBy: sql.NullInt64{Int64: adminFrom(r).ID, Valid: true}})
		if !isUnique(err) {
			return err
		}
	}
	return errors.New("could not find a free voucher code")
}

func (s *Server) vchDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if v, err := s.queries.GetVoucher(r.Context(), pathID(r)); err == nil {
		name = v.Code
	}
	s.remove(w, r, "/admin/vouchers", "voucher", "Voucher is in use", name, func(id int64) error {
		return s.queries.DeleteVoucher(r.Context(), id)
	})
}

type printItem struct {
	Code, Plan, Price string
	QR                template.URL
}

type printPage struct {
	Company string
	Items   []printItem
}

// vchPrint lays out the newest unused vouchers (optionally of one plan) for printing.
func (s *Server) vchPrint(w http.ResponseWriter, r *http.Request) {
	planID, _ := strconv.ParseInt(r.URL.Query().Get("plan"), 10, 64)
	limit, ok := posInt(r.URL.Query().Get("limit"))
	if !ok || limit > maxVouchers {
		limit = 36
	}
	rows, err := s.queries.SearchVouchers(r.Context(), db.SearchVouchersParams{Status: "unused", PlanID: planID, PageLimit: limit})
	if err != nil {
		s.fail(w, "print vouchers", err)
		return
	}
	_, plans, err := s.planOptions(r, false)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	settings, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	pp := printPage{Company: settings["company_name"]}
	for _, v := range rows {
		png, err := qrcode.Encode(v.Code, qrcode.Medium, 160)
		if err != nil {
			slog.Error("qr", "err", err)
			continue
		}
		p := plans[v.PlanID]
		pp.Items = append(pp.Items, printItem{v.Code, p.Name, money(p.Price),
			template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))})
	}
	s.render(w, r, 200, "print", Page{Title: "Voucher", Data: pp})
}

func (s *Server) vchRedeemForm(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"customer": r.URL.Query().Get("customer")}
	s.renderForm(w, r, 200, redeemPage(v, nil))
}

func redeemPage(v, e map[string]string) formPage {
	return formPage{"Redeem Voucher", "/admin/vouchers/redeem", "/admin/vouchers",
		[]field{text("customer", "Username", v, e).req(), text("code", "Code Voucher", v, e).req()}}
}

func (s *Server) vchRedeem(w http.ResponseWriter, r *http.Request) {
	v := formVals(r, "customer", "code")
	e := map[string]string{}
	c, err := s.queries.GetCustomerByUsername(r.Context(), v["customer"])
	if err == sql.ErrNoRows {
		e["customer"] = "Customer not found"
	} else if err != nil {
		s.fail(w, "get customer", err)
		return
	}
	if v["code"] == "" {
		e["code"] = "This field is required"
	}
	if len(e) == 0 {
		if s.Billing == nil {
			s.fail(w, "redeem voucher", errors.New("billing service not configured"))
			return
		}
		switch err := s.Billing.RedeemVoucher(r.Context(), v["code"], c.ID); {
		case errors.Is(err, billing.ErrVoucherInvalid):
			e["code"] = "Voucher not valid or already used"
		case err != nil:
			s.fail(w, "redeem voucher", err)
			return
		default:
			s.done(w, r, fmt.Sprint("/admin/customers/", c.ID), "Voucher redeemed", "voucher.redeem", v["code"]+" -> "+c.Username)
			return
		}
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, redeemPage(v, e))
}

func (s *Server) trxList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchTransactions(r.Context(), db.SearchTransactionsParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list transactions", err)
		return
	}
	lp := listPage{Heading: "Transactions", Base: "/admin/transactions", Q: q, Searchable: true,
		Cols: []string{"Invoice", "Date", "Username", "Plan Name", "Type", "Method", "Plan Price"}}
	for _, t := range rows {
		lp.Rows = append(lp.Rows, listRow{t.ID, []string{t.Invoice, s.ts(t.CreatedAt), t.Username, t.PlanName, t.Type, t.Method, money(t.Price)}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}
