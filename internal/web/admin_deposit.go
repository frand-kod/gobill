package web

// Admin balance deposit form and save.

import (
	"database/sql"
	"net/http"

	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/billing"
)

func depositPage(v, e map[string]string, plans []option) formPage {
	pl := text("plan", "Balance Package", v, e).as("select")
	pl.Options = append([]option{{"", "-"}}, plans...)
	return formPage{"Refill Balance", "/admin/deposit", "/admin/customers", section([]field{
		customerPick("customer", "Username", v, e).req().withBalance(),
		pl.hint("Pick a package, or leave empty and enter an amount"),
		text("amount", "Balance Amount", v, e).as("number"),
		text("note", "Note", v, e).as("textarea"),
	}, "Refill Balance", "")}
}

func (s *Server) balancePlans(r *http.Request) (opts []option, err error) {
	all, plans, err := s.planOptions(r, true)
	for _, o := range all {
		id, _ := posInt(o.Value)
		if plans[id].Type == "Balance" {
			opts = append(opts, o)
		}
	}
	return opts, err
}

func (s *Server) depositForm(w http.ResponseWriter, r *http.Request) {
	opts, err := s.balancePlans(r)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	s.renderForm(w, r, 200, depositPage(map[string]string{"customer": r.URL.Query().Get("customer")}, nil, opts))
}

func (s *Server) depositSave(w http.ResponseWriter, r *http.Request) {
	opts, err := s.balancePlans(r)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	v, e := formVals(r, "customer", "plan", "amount", "note"), map[string]string{}
	c, err := s.queries.GetCustomerByUsername(r.Context(), v["customer"])
	if err == sql.ErrNoRows {
		e["customer"] = "Customer not found"
	} else if err != nil {
		s.fail(w, "get customer", err)
		return
	}
	planID, _ := posInt(v["plan"])
	amount, _ := posInt(v["amount"])
	if planID == 0 && amount == 0 {
		e["amount"] = "Choose a Balance package or enter an amount"
	}
	if len(e) == 0 {
		if s.Billing == nil {
			s.fail(w, "deposit", errors.New("billing service not configured"))
			return
		}
		// the service writes the transaction and the activity log
		switch err := s.Billing.Deposit(r.Context(), c.ID, planID, amount, v["note"], adminFrom(r).ID); {
		case errors.Is(err, billing.ErrBadDeposit):
			e["plan"] = "Invalid value"
		case err != nil:
			s.fail(w, "deposit", err)
			return
		default:
			msg := s.catalog.T(s.language(), "Refill Balance")
			if nc, err := s.queries.GetCustomer(r.Context(), c.ID); err == nil { // amount = what the balance actually grew by
				msg = fmt.Sprintf(s.catalog.T(s.language(), "Balance +%s, now %s"), money(nc.Balance-c.Balance), money(nc.Balance))
			}
			s.sessions.Put(r.Context(), "flash", msg+": "+c.Username)
			http.Redirect(w, r, fmt.Sprint("/admin/customers/", c.ID), http.StatusSeeOther)
			return
		}
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, depositPage(v, e, opts))
}
