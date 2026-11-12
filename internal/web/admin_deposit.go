package web

// Admin balance deposit form and save.

import (
	"database/sql"
	"net/http"
	"net/url"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
)

// Money forms (deposit, single recharge) carry a one-time token: a second POST with the same token
// (double click, back + resend) is refused and charges nothing. Tokens live in memory; a restart
// only makes an open form ask to be reloaded.
var formTokens = struct {
	sync.Mutex
	m map[string]time.Time
}{m: map[string]time.Time{}}

const msgFormReplayed = "This form was already submitted or has expired. Nothing was charged. Open the form again."

func newFormToken() string {
	tok, err := newBulkToken()
	if err != nil {
		return "" // an empty token is never accepted, so the form simply fails closed
	}
	formTokens.Lock()
	defer formTokens.Unlock()
	now := time.Now()
	for k, exp := range formTokens.m { // ponytail: linear sweep, fine for a handful of admins
		if now.After(exp) {
			delete(formTokens.m, k)
		}
	}
	formTokens.m[tok] = now.Add(2 * time.Hour)
	return tok
}

// takeFormToken consumes tok; it reports true only the first time a live token is presented.
func takeFormToken(tok string) bool {
	formTokens.Lock()
	defer formTokens.Unlock()
	exp, ok := formTokens.m[tok]
	delete(formTokens.m, tok)
	return ok && time.Now().Before(exp)
}

func depositPage(v, e map[string]string, plans []option) formPage {
	pl := text("plan", "Balance Package", v, e).As("select")
	pl.Options = append([]option{{Value: "", Label: "-"}}, plans...)
	return formPage{Heading: "Refill Balance", Action: "/admin/deposit", Cancel: "/admin/customers", Fields: section([]field{
		customerPick("customer", "Username", v, e).Req().WithBalance(),
		pl.WithHint("Pick a package, or leave empty and enter an amount"),
		numeric(text("amount", "Balance Amount", v, e).As("number")),
		text("note", "Note", v, e).As("textarea"),
		{Name: "token", Type: "hidden", Value: newFormToken()},
	}, "Refill Balance", "")}
}

func numeric(f field) field { f.Numeric = true; return f }

// depositConfirm is the "are you sure" page: what will be added, to whom, and the balance after.
type depositConfirm struct {
	Summary                             string
	Customer, Plan, Amount, Note, Token string
}

func (s *Server) balancePlans(r *http.Request) (opts []option, err error) {
	all, plans, err := s.planOptions(r, true)
	for _, o := range all {
		id, _ := httpx.PosInt(o.Value)
		if plans[id].Type == "Balance" {
			opts = append(opts, o)
		}
	}
	return opts, err
}

func (s *Server) depositForm(w http.ResponseWriter, r *http.Request) {
	opts, err := s.balancePlans(r)
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	s.renderForm(w, r, 200, depositPage(map[string]string{"customer": r.URL.Query().Get("customer")}, nil, opts))
}

func (s *Server) depositSave(w http.ResponseWriter, r *http.Request) {
	opts, err := s.balancePlans(r)
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	confirmed := r.PostFormValue("confirmed") == "1"
	if confirmed && !takeFormToken(r.PostFormValue("token")) {
		s.Sessions.Put(r.Context(), "error", s.T(msgFormReplayed))
		http.Redirect(w, r, "/admin/deposit?customer="+url.QueryEscape(r.PostFormValue("customer")), http.StatusSeeOther)
		return
	}
	v, e := httpx.FormVals(r, "customer", "plan", "amount", "note"), map[string]string{}
	c, err := s.Queries.GetCustomerByUsername(r.Context(), v["customer"])
	if err == sql.ErrNoRows {
		e["customer"] = "Customer not found"
	} else if err != nil {
		s.Fail(w, "get customer", err)
		return
	}
	planID, _ := httpx.PosInt(v["plan"])
	amount, _ := httpx.PosInt(v["amount"])
	switch {
	case wholeErr(v["amount"]) != "":
		e["amount"] = wholeErr(v["amount"])
	case planID != 0 && amount != 0:
		e["amount"] = "Choose a package or enter an amount, not both"
	case planID == 0 && amount == 0:
		e["amount"] = "Choose a Balance package or enter an amount"
	}
	if len(e) == 0 && !confirmed {
		if planID > 0 {
			pl, err := s.Queries.GetPlan(r.Context(), planID)
			if err != nil || pl.Type != "Balance" {
				e["plan"] = "Invalid value"
			} else {
				amount = pl.Price
			}
		}
		if len(e) == 0 {
			d := depositConfirm{Customer: c.Username, Plan: v["plan"], Amount: v["amount"], Note: v["note"], Token: r.PostFormValue("token")}
			d.Summary = fmt.Sprintf(s.T("Add %s to %s; balance becomes %s"), view.Money(amount), c.Username, view.Money(c.Balance+amount))
			s.Render(w, r, 200, "deposit_confirm", Page{Title: "Refill Balance", Data: d})
			return
		}
	}
	if len(e) == 0 {
		if s.Billing == nil {
			s.Fail(w, "deposit", errors.New("billing service not configured"))
			return
		}
		// the service writes the transaction and the activity log
		switch err := s.Billing.Deposit(r.Context(), c.ID, planID, amount, v["note"], adminFrom(r).ID); {
		case errors.Is(err, billing.ErrBadDeposit):
			e["plan"] = "Invalid value"
		case err != nil:
			s.Fail(w, "deposit", err)
			return
		default:
			msg := s.T("Refill Balance")
			if nc, err := s.Queries.GetCustomer(r.Context(), c.ID); err == nil { // amount = what the balance actually grew by
				msg = fmt.Sprintf(s.T("Balance +%s, now %s"), view.Money(nc.Balance-c.Balance), view.Money(nc.Balance))
			}
			s.Sessions.Put(r.Context(), "flash", msg+": "+c.Username)
			s.Sessions.Put(r.Context(), "flash_sticky", "1") // the receipt for money moved: no auto-dismiss
			http.Redirect(w, r, fmt.Sprint("/admin/customers/", c.ID), http.StatusSeeOther)
			return
		}
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, depositPage(v, e, opts))
}
