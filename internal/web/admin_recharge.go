package web

// Admin-side customer recharge with confirmation.

import (
	"bytes"
	"database/sql"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"strings"
)

type rechargeConfirm struct {
	C                      db.Customer
	Plan, Method           string
	MethodLabel            string
	PlanID                 int64
	Price, Expiry, Extends string
	Balance, After         string
	Insufficient           bool
	Fragment               bool // the card alone, for the recharge dialog (X-Fragment: 1)
}

// isFragment: the recharge forms post with X-Fragment: 1 and show the answer in a dialog.
func isFragment(r *http.Request) bool { return r.Header.Get("X-Fragment") == "1" }

// rechargeErrors answers a fragment request with the messages to show inside the dialog (422).
func (s *Server) rechargeErrors(w http.ResponseWriter, msgs ...string) {
	for i, m := range msgs {
		msgs[i] = s.catalog.T(s.language(), m)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := s.templates["recharge_confirm"].ExecuteTemplate(w, "recharge_error", msgs); err != nil {
		slog.Error("recharge errors", "err", err)
	}
}

// custRechargeConfirm shows what the recharge will do (old recharge-confirm); it writes nothing.
func (s *Server) custRechargeConfirm(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	back := fmt.Sprint("/admin/customers/", c.ID)
	fail := func(msg string) {
		if isFragment(r) {
			s.rechargeErrors(w, msg)
			return
		}
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), msg))
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	if s.Billing == nil {
		s.fail(w, "recharge", errors.New("billing service not configured"))
		return
	}
	planID, _ := posInt(r.PostFormValue("plan"))
	method, label, ok := s.rechargeMethod(r)
	if !ok {
		fail("Invalid payment method")
		return
	}
	pv, err := s.Billing.Preview(r.Context(), c.ID, planID)
	if err != nil || pv.Plan.Enabled != 1 && !oneOf(adminFrom(r).Role, "SuperAdmin", "Admin") {
		pl, perr := s.queries.GetPlan(r.Context(), planID)
		fail(planErrMsg(perr == nil, pl.Enabled))
		return
	}
	d := rechargeConfirm{C: c, Plan: pv.Plan.Name, PlanID: planID, Method: method, MethodLabel: label, Price: money(pv.Price), Balance: money(c.Balance), After: money(c.Balance), Fragment: isFragment(r)}
	if !pv.Expiry.IsZero() {
		d.Expiry = pv.Expiry.In(s.location()).Format("2006-01-02 15:04")
	}
	if method == methodZero {
		pv.Price, d.Price = 0, money(0)
	}
	if pv.Plan.Type == "Balance" {
		d.After = money(c.Balance + pv.Price)
	}
	if method == "Balance" {
		d.After, d.Insufficient = money(c.Balance-pv.Price), c.Balance < pv.Price
	}
	if d.Fragment {
		var buf bytes.Buffer
		if err := s.templates["recharge_confirm"].ExecuteTemplate(&buf, "recharge_card", d); err != nil {
			s.fail(w, "recharge card", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(buf.Bytes())
		return
	}
	s.render(w, r, 200, "recharge_confirm", Page{Title: "Recharge Account", Data: d})
}

// custRecharge activates a plan for the customer, paid in cash or from their balance.
func (s *Server) custRecharge(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	back := fmt.Sprint("/admin/customers/", c.ID)
	fail := func(msg string) {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), msg))
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	if s.Billing == nil {
		s.fail(w, "recharge", errors.New("billing service not configured"))
		return
	}
	planID, _ := posInt(r.PostFormValue("plan"))
	plan, err := s.queries.GetPlan(r.Context(), planID)
	if err != nil || plan.Enabled != 1 && !oneOf(adminFrom(r).Role, "SuperAdmin", "Admin") {
		fail(planErrMsg(err == nil, plan.Enabled))
		return
	}
	admin := adminFrom(r)
	method, label, ok := s.rechargeMethod(r)
	if !ok {
		fail("Invalid payment method")
		return
	}
	// PHP records "<method> - <admin name>" (Recharge Zero for free ones); balance payments keep
	// "Customer - Balance" so the dashboard can leave them out (S1).
	rec := titleWords(label) + " - " + admin.Fullname
	ctx, devFailed := billing.TrackDeviceFailure(r.Context())
	switch method {
	case methodZero:
		err = s.Billing.RechargeZero(ctx, c.ID, plan.ID, "Recharge Zero - "+admin.Fullname, admin.ID)
	case "Balance":
		err = s.Billing.RechargeWithBalance(ctx, c.ID, plan.ID, admin.ID)
	default:
		err = s.Billing.Recharge(ctx, c.ID, plan.ID, rec, admin.ID)
	}
	if errors.Is(err, billing.ErrInsufficientBalance) {
		s.sessions.Put(r.Context(), "errlink", "/admin/deposit?customer="+url.QueryEscape(c.Username))
		fail(msgNoBalance)
		return
	} else if errors.Is(err, billing.ErrInactive) {
		fail("account is not active")
		return
	} else if err != nil {
		slog.Error("recharge", "customer", c.Username, "plan", plan.Name, "err", err)
		fail(msgRechargeFail)
		return
	}
	if *devFailed { // money and transaction are saved; only the router missed the change
		s.logActivity(r, "customer.recharge", c.Username+" ["+plan.Name+"] router not updated")
		s.sessions.Put(r.Context(), "warn", s.catalog.T(s.language(), msgNotSynced))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.done(w, r, back, "Recharge Successful", "customer.recharge", c.Username+" ["+plan.Name+"]")
}

const methodZero = "Zero"

// rechargeMethods lists what the admin recharge form offers: the payment_usings setting (PHP
// plan.php:80, default Cash), then Balance and Recharge Zero.
func (s *Server) rechargeMethods(ctx context.Context) []option {
	st, _ := s.loadSettings(ctx)
	var out []option
	seen := map[string]bool{"balance": true, "zero": true}
	for _, u := range strings.Split(st["payment_usings"], ",") {
		if u = strings.TrimSpace(u); u != "" && !seen[strings.ToLower(u)] {
			seen[strings.ToLower(u)] = true
			out = append(out, option{u, u})
		}
	}
	if len(out) == 0 {
		out = append(out, option{"Cash", "Cash"})
	}
	return append(out, option{"Balance", "Balance"}, option{methodZero, "Recharge Zero"})
}

// rechargeMethod validates the posted method against rechargeMethods; label is its display name.
func (s *Server) rechargeMethod(r *http.Request) (value, label string, ok bool) {
	v := r.PostFormValue("method")
	for _, o := range s.rechargeMethods(r.Context()) {
		if o.Value == v {
			return v, o.Label, true
		}
	}
	return "", "", false
}

// rechargePage is the Recharge Account page: pick a customer and a plan, then go on to the confirm page.
type rechargePage struct {
	Customer, Plan, Method          string
	Plans, Methods                  []option
	ErrCustomer, ErrPlan, ErrMethod string
	Summary                         string // JSON labels for the customer summary panel
}

// CustomerField is the customer picker for the recharge form (name "customer", as before).
func (p rechargePage) CustomerField() field {
	return field{Name: "customer", Label: "Username", Type: "customer", Value: p.Customer, Error: p.ErrCustomer}
}

// rechargeOptions lists the plans this role may recharge (same rule as the customer page).
func (s *Server) rechargeOptions(r *http.Request) ([]option, error) {
	manage := oneOf(adminFrom(r).Role, "SuperAdmin", "Admin")
	plans, _, err := s.planOptions(r, !manage) // PHP: SuperAdmin/Admin may recharge a disabled plan
	return plans, err
}

func (s *Server) rechargeForm(w http.ResponseWriter, r *http.Request, status int, d rechargePage, plans []option) {
	d.Plans, d.Methods, d.Summary = plans, s.rechargeMethods(r.Context()), s.summaryLabels()
	s.render(w, r, status, "recharge", Page{Title: "Recharge Account", Data: d})
}

// rechargePick: GET /admin/recharge, optionally ?customer=<username> to prefill.
func (s *Server) rechargePick(w http.ResponseWriter, r *http.Request) {
	plans, err := s.rechargeOptions(r)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	s.rechargeForm(w, r, http.StatusOK, rechargePage{Customer: strings.TrimSpace(r.URL.Query().Get("customer"))}, plans)
}

// rechargeStart: POST /admin/recharge. Resolves the username, checks the plan and method, then
// shows the existing confirm page for that customer. It writes nothing.
func (s *Server) rechargeStart(w http.ResponseWriter, r *http.Request) {
	plans, err := s.rechargeOptions(r)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	d := rechargePage{Customer: strings.TrimSpace(r.PostFormValue("customer")), Plan: r.PostFormValue("plan"), Method: r.PostFormValue("method")}
	c, err := s.queries.GetCustomerByUsername(r.Context(), d.Customer)
	switch {
	case d.Customer == "":
		d.ErrCustomer = "Choose a customer first"
	case errors.Is(err, sql.ErrNoRows):
		d.ErrCustomer = "Customer not found"
	case err != nil:
		s.fail(w, "get customer", err)
		return
	case c.Status != "Active":
		d.ErrCustomer = "This customer is not active. Turn the customer on first."
	}
	allowed := false
	for _, o := range plans {
		allowed = allowed || o.Value == d.Plan
	}
	switch {
	case d.Plan == "":
		d.ErrPlan = "Choose a service plan first"
	case !allowed:
		d.ErrPlan = msgPlanMissing
	}
	if _, _, ok := s.rechargeMethod(r); !ok {
		d.ErrMethod = "Invalid payment method"
	}
	if d.ErrCustomer != "" || d.ErrPlan != "" || d.ErrMethod != "" {
		if isFragment(r) {
			var msgs []string
			for _, m := range []string{d.ErrCustomer, d.ErrPlan, d.ErrMethod} {
				if m != "" {
					msgs = append(msgs, m)
				}
			}
			s.rechargeErrors(w, msgs...)
			return
		}
		s.rechargeForm(w, r, http.StatusUnprocessableEntity, d, plans)
		return
	}
	r.SetPathValue("id", strconv.FormatInt(c.ID, 10)) // the confirm handler reads the customer from the path
	s.custRechargeConfirm(w, r)
}
