package billing

// Admin-side customer recharge with confirmation.

import (
	"bytes"
	"database/sql"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"errors"
	"fmt"
	"strings"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
)

type rechargeConfirm struct {
	C                      db.Customer
	Plan, Method           string
	MethodLabel            string
	PlanID                 int64
	Price, Expiry, Extends string
	Balance, After         string
	Insufficient           bool
	Zero                   bool // Recharge Zero: recorded as Rp 0, the warning shows
	Fragment               bool // the card alone, for the recharge dialog (X-Fragment: 1)
	Token                  string
}

// isFragment: the recharge forms post with X-Fragment: 1 and show the answer in a dialog.
func isFragment(r *http.Request) bool { return r.Header.Get("X-Fragment") == "1" }

// rechargeErrors answers a fragment request with the messages to show inside the dialog (422).
func (s *Handlers) rechargeErrors(w http.ResponseWriter, msgs ...string) {
	for i, m := range msgs {
		msgs[i] = s.T(m)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := s.Templates["recharge_confirm"].ExecuteTemplate(w, "recharge_error", msgs); err != nil {
		slog.Error("recharge errors", "err", err)
	}
}

// custRechargeConfirm shows what the recharge will do (old recharge-confirm); it writes nothing.
func (s *Handlers) custRechargeConfirm(w http.ResponseWriter, r *http.Request) {
	c, ok := s.CustGet(w, r)
	if !ok {
		return
	}
	back := fmt.Sprint("/admin/customers/", c.ID)
	fail := func(msg string) {
		if isFragment(r) {
			s.rechargeErrors(w, msg)
			return
		}
		s.Sessions.Put(r.Context(), "error", s.T(msg))
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	if s.Billing == nil {
		s.Fail(w, "recharge", errors.New("billing service not configured"))
		return
	}
	planID, _ := httpx.PosInt(r.PostFormValue("plan"))
	method, label, ok := s.rechargeMethod(r)
	if !ok {
		fail("Invalid payment method")
		return
	}
	pv, err := s.Billing.Preview(r.Context(), c.ID, planID)
	if err != nil || pv.Plan.Enabled != 1 && !httpx.OneOf(adminx.AdminFrom(r).Role, "SuperAdmin", "Admin") {
		pl, perr := s.Queries.GetPlan(r.Context(), planID)
		fail(adminx.PlanErrMsg(perr == nil, pl.Enabled))
		return
	}
	d := rechargeConfirm{C: c, Plan: pv.Plan.Name, PlanID: planID, Method: method, MethodLabel: label, Price: view.Money(pv.Price), Balance: view.Money(c.Balance), After: view.Money(c.Balance), Fragment: isFragment(r), Token: NewFormToken()}
	if !pv.Expiry.IsZero() {
		d.Expiry = pv.Expiry.In(s.Location()).Format("2006-01-02 15:04")
	}
	if method == adminx.MethodZero {
		pv.Price, d.Price, d.Zero = 0, view.Money(0), true
	}
	if pv.Plan.Type == "Balance" {
		d.After = view.Money(c.Balance + pv.Price)
	}
	if method == "Balance" {
		d.After, d.Insufficient = view.Money(c.Balance-pv.Price), c.Balance < pv.Price
	}
	if d.Fragment {
		var buf bytes.Buffer
		if err := s.Templates["recharge_confirm"].ExecuteTemplate(&buf, "recharge_card", d); err != nil {
			s.Fail(w, "recharge card", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(buf.Bytes())
		return
	}
	s.Render(w, r, 200, "recharge_confirm", core.Page{Title: "Recharge Account", Data: d})
}

// custRecharge activates a plan for the customer, paid in cash or from their balance.
func (s *Handlers) custRecharge(w http.ResponseWriter, r *http.Request) {
	c, ok := s.CustGet(w, r)
	if !ok {
		return
	}
	back := fmt.Sprint("/admin/customers/", c.ID)
	fail := func(msg string) {
		s.Sessions.Put(r.Context(), "error", s.T(msg))
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	if s.Billing == nil {
		s.Fail(w, "recharge", errors.New("billing service not configured"))
		return
	}
	if !takeFormToken(r.PostFormValue("token")) {
		fail(msgFormReplayed)
		return
	}
	planID, _ := httpx.PosInt(r.PostFormValue("plan"))
	plan, err := s.Queries.GetPlan(r.Context(), planID)
	if err != nil || plan.Enabled != 1 && !httpx.OneOf(adminx.AdminFrom(r).Role, "SuperAdmin", "Admin") {
		fail(adminx.PlanErrMsg(err == nil, plan.Enabled))
		return
	}
	method, label, ok := s.rechargeMethod(r)
	if !ok {
		fail("Invalid payment method")
		return
	}
	devFailed, err := s.ChargePlan(r, c, plan, method, label)
	if errors.Is(err, billing.ErrInsufficientBalance) {
		s.Sessions.Put(r.Context(), "errlink", "/admin/deposit?customer="+url.QueryEscape(c.Username))
	}
	if err != nil {
		fail(adminx.RechargeFailMsg(err, c, plan))
		return
	}
	if devFailed { // money and transaction are saved; only the router missed the change
		s.LogActivity(r, "customer.recharge", c.Username+" ["+plan.Name+"] router not updated")
		s.Sessions.Put(r.Context(), "warn", s.T(adminx.MsgNotSynced))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.Done(w, r, back, "Recharge Successful", "customer.recharge", c.Username+" ["+plan.Name+"]")
}

// rechargeMethod validates the posted method against rechargeMethods; label is its display name.
func (s *Handlers) rechargeMethod(r *http.Request) (value, label string, ok bool) {
	v := r.PostFormValue("method")
	for _, o := range s.RechargeMethods(r.Context()) {
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
func (s *Handlers) rechargeOptions(r *http.Request) ([]option, error) {
	manage := httpx.OneOf(adminx.AdminFrom(r).Role, "SuperAdmin", "Admin")
	plans, _, err := s.PlanOptions(r, !manage) // PHP: SuperAdmin/Admin may recharge a disabled plan
	return plans, err
}

func (s *Handlers) rechargeForm(w http.ResponseWriter, r *http.Request, status int, d rechargePage, plans []option) {
	d.Plans, d.Methods, d.Summary = plans, s.RechargeMethods(r.Context()), s.SummaryLabels()
	s.Render(w, r, status, "recharge", core.Page{Title: "Recharge Account", Data: d})
}

// rechargePick: GET /admin/recharge, optionally ?customer=<username> to prefill.
func (s *Handlers) rechargePick(w http.ResponseWriter, r *http.Request) {
	plans, err := s.rechargeOptions(r)
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	s.rechargeForm(w, r, http.StatusOK, rechargePage{Customer: strings.TrimSpace(r.URL.Query().Get("customer"))}, plans)
}

// rechargeStart: POST /admin/recharge. Resolves the username, checks the plan and method, then
// shows the existing confirm page for that customer. It writes nothing.
func (s *Handlers) rechargeStart(w http.ResponseWriter, r *http.Request) {
	plans, err := s.rechargeOptions(r)
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	d := rechargePage{Customer: strings.TrimSpace(r.PostFormValue("customer")), Plan: r.PostFormValue("plan"), Method: r.PostFormValue("method")}
	c, err := s.Queries.GetCustomerByUsername(r.Context(), d.Customer)
	switch {
	case d.Customer == "":
		d.ErrCustomer = "Choose a customer first"
	case errors.Is(err, sql.ErrNoRows):
		d.ErrCustomer = "Customer not found"
	case err != nil:
		s.Fail(w, "get customer", err)
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
		d.ErrPlan = adminx.MsgPlanMissing
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
