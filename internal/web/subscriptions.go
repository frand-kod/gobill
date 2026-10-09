package web

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
)

const localTime = "2006-01-02T15:04" // <input type="datetime-local">

// subQuery is the filter shared by the list and the CSV export; paging is added by the caller.
func subQuery(r *http.Request) db.FilterSubscriptionsParams {
	g := r.URL.Query().Get
	p := db.FilterSubscriptionsParams{Q: strings.TrimSpace(g("q")), Status: g("status"), Type: g("type")}
	if !oneOf(p.Status, "active", "expired") {
		p.Status = ""
	}
	if !oneOf(p.Type, "Hotspot", "PPPoE") {
		p.Type = ""
	}
	p.RouterID, _ = posInt(g("router"))
	p.PlanID, _ = posInt(g("plan"))
	return p
}

func (s *Server) subList(w http.ResponseWriter, r *http.Request) {
	_, page, limit, off := paging(r)
	p := subQuery(r)
	p.PageLimit, p.PageOffset = limit, off
	g := r.URL.Query().Get
	rts, err := s.routerNames(r)
	if err != nil {
		s.fail(w, "list routers", err)
		return
	}
	routers := []option{{"", "Router"}}
	for _, x := range rts {
		routers = append(routers, option{fmt.Sprint(x.ID), x.Name})
	}
	sort, dir, param := listSort(r, "username", "plan", "created", "expires")
	p.Sort = param
	rows, err := s.queries.FilterSubscriptions(r.Context(), p)
	if err != nil {
		s.fail(w, "list subscriptions", err)
		return
	}
	planOpts, _, err := s.planOptions(r, false)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	plans := append([]option{{"", "Plan"}}, planOpts...)
	lp := listPage{Heading: "Subscriptions", Base: "/admin/subscriptions", Q: p.Q, Searchable: true,
		CanEdit: oneOf(adminFrom(r).Role, "SuperAdmin", "Admin", "Agent", "Sales"), NoDelete: true,
		DeleteOnly: !oneOf(adminFrom(r).Role, "SuperAdmin", "Admin"), // Agent/Sales get Extend only (PHP plan/extend has no role check)
		Cols:       []string{"Username", "Plan Name", "Type", "Created On", "Expires On", "Method", "Location", "Status"},
		SortKeys:   []string{"username", "plan", "", "created", "expires", "", "", ""}, Sort: sort, Dir: dir,
		Filters: []filter{{"status", p.Status, []option{{"", "Status"}, {"active", "active"}, {"expired", "expired"}}},
			{"type", p.Type, anyOpts("Type", "Hotspot", "PPPoE")}, {"router", g("router"), routers}, {"plan", g("plan"), plans}},
		Actions: []rowAction{{"extend", "Extend", "days", true}, {"deactivate", "Deactivate", "", true}, {"sync", "Sync", "", false}}}
	if lp.DeleteOnly {
		lp.Actions = lp.Actions[:1]
	}
	for _, x := range rows {
		lp.Rows = append(lp.Rows, listRow{x.ID, []string{x.Username, x.PlanName, x.Type, s.ts(x.StartedAt), s.ts(x.ExpiresAt), x.Method, x.RouterName, x.Status}})
	}
	lp.Links = []option{{"/admin/subscriptions/export?" + lp.query().Encode(), "Export CSV"}}
	lp.finish(page)
	s.renderList(w, r, lp)
}

// subExport streams the current filter (all pages) as CSV, like custExport.
func (s *Server) subExport(w http.ResponseWriter, r *http.Request) {
	p := subQuery(r)
	p.PageLimit = -1
	rows, err := s.queries.FilterSubscriptions(r.Context(), p)
	if err != nil {
		s.fail(w, "export subscriptions", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="subscriptions-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Username", "Plan Name", "Type", "Created On", "Expires On", "Method", "Location", "Status"})
	for _, x := range rows {
		rec := []string{x.Username, x.PlanName, x.Type, s.ts(x.StartedAt), s.ts(x.ExpiresAt), x.Method, x.RouterName, x.Status}
		csvSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}

func (s *Server) subFields(r *http.Request, v, e map[string]string) ([]field, error) {
	opts, plans, err := s.planOptions(r, false)
	if err != nil {
		return nil, err
	}
	pl := text("plan", "Service Plan", v, e).as("select").req()
	for _, o := range opts {
		id, _ := posInt(o.Value)
		if plans[id].Type != "Balance" {
			pl.Options = append(pl.Options, o)
		}
	}
	return section([]field{pl, text("expires_at", "Expires On", v, e).as("datetime-local").req()}, "Subscription", ""), nil
}

func (s *Server) subGet(w http.ResponseWriter, r *http.Request) (sub db.Subscription, username string, ok bool) {
	sub, err := s.queries.GetSubscription(r.Context(), pathID(r))
	if err == nil {
		var c db.Customer
		if c, err = s.queries.GetCustomer(r.Context(), sub.CustomerID); err == nil {
			return sub, c.Username, true
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
	} else {
		s.fail(w, "get subscription", err)
	}
	return sub, "", false
}

func (s *Server) subEdit(w http.ResponseWriter, r *http.Request) {
	sub, user, ok := s.subGet(w, r)
	if !ok {
		return
	}
	v := map[string]string{"plan": fmt.Sprint(sub.PlanID), "expires_at": time.Unix(sub.ExpiresAt, 0).In(s.location()).Format(localTime)}
	s.subForm(w, r, 200, sub.ID, user, v, nil)
}

func (s *Server) subForm(w http.ResponseWriter, r *http.Request, code int, id int64, user string, v, e map[string]string) {
	fs, err := s.subFields(r, v, e)
	if err != nil {
		s.fail(w, "subscription form", err)
		return
	}
	s.renderForm(w, r, code, formPage{"Edit Subscription: " + user, fmt.Sprint("/admin/subscriptions/", id), "/admin/subscriptions", fs})
}

func (s *Server) subSave(w http.ResponseWriter, r *http.Request) {
	sub, user, ok := s.subGet(w, r)
	if !ok {
		return
	}
	v, e := formVals(r, "plan", "expires_at"), map[string]string{}
	planID, valid := posInt(v["plan"])
	if !valid {
		e["plan"] = "Invalid value"
	}
	exp, err := time.ParseInLocation(localTime, v["expires_at"], s.location())
	if err != nil {
		e["expires_at"] = "Invalid value"
	}
	if len(e) == 0 {
		if s.Billing == nil {
			s.fail(w, "edit subscription", errors.New("billing service not configured"))
			return
		}
		if err = s.Billing.EditSubscription(r.Context(), sub.ID, planID, exp, adminFrom(r).ID); err != nil {
			slog.Error("edit subscription", "id", sub.ID, "err", err)
			e["expires_at"] = "Could not save, check the plan and the date"
		} else {
			s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), "Data Updated Successfully"))
			http.Redirect(w, r, "/admin/subscriptions", http.StatusSeeOther)
			return
		}
	}
	s.subForm(w, r, http.StatusUnprocessableEntity, sub.ID, user, v, e)
}

// subAct runs a billing action on a subscription and flashes the outcome. The service writes
// the activity log.
func (s *Server) subAct(w http.ResponseWriter, r *http.Request, do func(id int64) error) {
	back := "/admin/subscriptions"
	msg := "Data Updated Successfully"
	if s.Billing == nil {
		s.fail(w, "subscription action", errors.New("billing service not configured"))
		return
	}
	key := "flash"
	if err := do(pathID(r)); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		slog.Error("subscription action", "path", r.URL.Path, "err", err)
		key, msg = "error", "Action failed"
	}
	s.sessions.Put(r.Context(), key, s.catalog.T(s.language(), msg))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) subExtend(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.PostFormValue("days"))
	if err != nil || days < 1 || days > 3650 {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Enter a number greater than 0"))
		http.Redirect(w, r, "/admin/subscriptions", http.StatusSeeOther)
		return
	}
	s.subAct(w, r, func(id int64) error { return s.Billing.ExtendSubscription(r.Context(), id, days, adminFrom(r).ID) })
}

func (s *Server) subDeactivate(w http.ResponseWriter, r *http.Request) {
	s.subAct(w, r, func(id int64) error { return s.Billing.DeactivateSubscription(r.Context(), id, adminFrom(r).ID) })
}

func (s *Server) subSync(w http.ResponseWriter, r *http.Request) {
	s.subAct(w, r, func(id int64) error { return s.Billing.SyncSubscription(r.Context(), id, adminFrom(r).ID) })
}

func depositPage(v, e map[string]string, plans []option) formPage {
	pl := text("plan", "Balance Package", v, e).as("select")
	pl.Options = append([]option{{"", "-"}}, plans...)
	return formPage{"Refill Balance", "/admin/deposit", "/admin/customers", section([]field{
		text("customer", "Username", v, e).req(),
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
