package web

// Subscription list, edit, extend, deactivate and sync.

import (
	"database/sql"
	"encoding/csv"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

const localTime = "2006-01-02T15:04" // <input type="datetime-local">

// subQuery is the filter shared by the list and the CSV export; paging is added by the caller.
func subQuery(r *http.Request) db.FilterSubscriptionsParams {
	g := r.URL.Query().Get
	p := db.FilterSubscriptionsParams{Q: strings.TrimSpace(g("q")), Status: g("status"), Type: g("type")}
	if !httpx.OneOf(p.Status, "active", "expired") {
		p.Status = ""
	}
	if !httpx.OneOf(p.Type, "Hotspot", "PPPoE") {
		p.Type = ""
	}
	p.RouterID, _ = httpx.PosInt(g("router"))
	p.PlanID, _ = httpx.PosInt(g("plan"))
	return p
}

func (s *Server) subList(w http.ResponseWriter, r *http.Request) {
	_, pg := view.Paging(r)
	p := subQuery(r)
	p.PageLimit, p.PageOffset = pg.Limit(), pg.Offset()
	g := r.URL.Query().Get
	rts, err := s.ax.RouterNames(r)
	if err != nil {
		s.Fail(w, "list routers", err)
		return
	}
	routers := []option{{Value: "", Label: "Router"}}
	for _, x := range rts {
		routers = append(routers, option{Value: fmt.Sprint(x.ID), Label: x.Name})
	}
	sort, dir, param := listSort(r, "username", "plan", "created", "expires")
	p.Sort = param
	rows, err := s.Queries.FilterSubscriptions(r.Context(), p)
	if err != nil {
		s.Fail(w, "list subscriptions", err)
		return
	}
	planOpts, _, err := s.planOptions(r, false)
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	plans := append([]option{{Value: "", Label: "Plan"}}, planOpts...)
	lp := listPage{Heading: "Subscriptions", Base: "/admin/subscriptions", Q: p.Q, Searchable: true,
		CanEdit: httpx.OneOf(adminFrom(r).Role, "SuperAdmin", "Admin", "Agent", "Sales"), NoDelete: true,
		DeleteOnly: !httpx.OneOf(adminFrom(r).Role, "SuperAdmin", "Admin"), // Agent/Sales get Extend only (PHP plan/extend has no role check)
		Cols:       []string{"Username", "Plan Name", "Type", "Created On", "Expires On", "Method", "Location", "Status"},
		SortKeys:   []string{"username", "plan", "", "created", "expires", "", "", ""}, Sort: sort, Dir: dir,
		Filters: []filter{{Name: "status", Val: p.Status, Opts: []option{{Value: "", Label: "Status"}, {Value: "active", Label: "active"}, {Value: "expired", Label: "expired"}}, Label: "Status"},
			{Name: "type", Val: p.Type, Opts: anyOpts("Type", "Hotspot", "PPPoE"), Label: "Type"}, {Name: "router", Val: g("router"), Opts: routers, Label: "Router"}, {Name: "plan", Val: g("plan"), Opts: plans, Label: "Plan"}},
		Actions: []rowAction{{Path: "extend", Label: "Extend", Field: "days", Confirm: true}, {Path: "deactivate", Label: "Deactivate", Field: "", Confirm: true}, {Path: "sync", Label: "Sync", Field: "", Confirm: false}}}
	if lp.DeleteOnly {
		lp.Actions = lp.Actions[:1]
	}
	if !s.canExtend(r) { // setting admin_extend
		lp.Actions = lp.Actions[1:]
	}
	for _, x := range rows {
		lp.Rows = append(lp.Rows, listRow{ID: x.ID, Cells: []string{x.Username, x.PlanName, x.Type, s.TS(x.StartedAt), s.SubExpiry(x.PendingStart, x.ExpiresAt), x.Method, x.RouterName, x.Status}})
	}
	lp.Links = []option{{Value: "/admin/subscriptions/export?" + lp.Query().Encode(), Label: "Export CSV"}}
	total, err := s.Queries.CountFilteredSubscriptions(r.Context(), db.CountFilteredSubscriptionsParams{Q: p.Q, Status: p.Status, Type: p.Type, RouterID: p.RouterID, PlanID: p.PlanID})
	if err != nil {
		s.Fail(w, "count subscriptions", err)
		return
	}
	lp.Finish(pg, total)
	s.renderList(w, r, lp)
}

// subExport streams the current filter (all pages) as CSV, like custExport.
func (s *Server) subExport(w http.ResponseWriter, r *http.Request) {
	p := subQuery(r)
	p.PageLimit = -1
	rows, err := s.Queries.FilterSubscriptions(r.Context(), p)
	if err != nil {
		s.Fail(w, "export subscriptions", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="subscriptions-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Username", "Plan Name", "Type", "Created On", "Expires On", "Method", "Location", "Status"})
	for _, x := range rows {
		rec := []string{x.Username, x.PlanName, x.Type, s.TS(x.StartedAt), s.TS(x.ExpiresAt), x.Method, x.RouterName, x.Status}
		httpx.CSVSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}

func (s *Server) subFields(r *http.Request, v, e map[string]string) ([]field, error) {
	opts, plans, err := s.planOptions(r, false)
	if err != nil {
		return nil, err
	}
	pl := text("plan", "Service Plan", v, e).As("select").Req()
	for _, o := range opts {
		id, _ := httpx.PosInt(o.Value)
		if plans[id].Type != "Balance" {
			pl.Options = append(pl.Options, o)
		}
	}
	return section([]field{pl, text("expires_at", "Expires On", v, e).As("datetime-local").Req()}, "Subscription", ""), nil
}

func (s *Server) subGet(w http.ResponseWriter, r *http.Request) (sub db.Subscription, username string, ok bool) {
	sub, err := s.Queries.GetSubscription(r.Context(), httpx.PathID(r))
	if err == nil {
		var c db.Customer
		if c, err = s.Queries.GetCustomer(r.Context(), sub.CustomerID); err == nil {
			return sub, c.Username, true
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
	} else {
		s.Fail(w, "get subscription", err)
	}
	return sub, "", false
}

func (s *Server) subEdit(w http.ResponseWriter, r *http.Request) {
	sub, user, ok := s.subGet(w, r)
	if !ok {
		return
	}
	v := map[string]string{"plan": fmt.Sprint(sub.PlanID), "expires_at": time.Unix(sub.ExpiresAt, 0).In(s.Location()).Format(localTime)}
	s.subForm(w, r, 200, sub.ID, user, v, nil)
}

func (s *Server) subForm(w http.ResponseWriter, r *http.Request, code int, id int64, user string, v, e map[string]string) {
	fs, err := s.subFields(r, v, e)
	if err != nil {
		s.Fail(w, "subscription form", err)
		return
	}
	s.renderForm(w, r, code, formPage{Heading: "Edit Subscription: " + user, Action: fmt.Sprint("/admin/subscriptions/", id), Cancel: "/admin/subscriptions", Fields: fs})
}

func (s *Server) subSave(w http.ResponseWriter, r *http.Request) {
	sub, user, ok := s.subGet(w, r)
	if !ok {
		return
	}
	v, e := httpx.FormVals(r, "plan", "expires_at"), map[string]string{}
	planID, valid := httpx.PosInt(v["plan"])
	if !valid {
		e["plan"] = "Invalid value"
	}
	exp, err := time.ParseInLocation(localTime, v["expires_at"], s.Location())
	if err != nil {
		e["expires_at"] = "Invalid value"
	} else if exp.Unix() == sub.ExpiresAt-sub.ExpiresAt%60 { // the form shows minutes only: an untouched date keeps its seconds (and the expiry notice)
		exp = time.Unix(sub.ExpiresAt, 0)
	}
	if len(e) == 0 {
		if s.Billing == nil {
			s.Fail(w, "edit subscription", errors.New("billing service not configured"))
			return
		}
		if err = s.Billing.EditSubscription(r.Context(), sub.ID, planID, exp, adminFrom(r).ID); err != nil {
			slog.Error("edit subscription", "id", sub.ID, "err", err)
			e["expires_at"] = "Could not save, check the plan and the date"
		} else {
			s.Sessions.Put(r.Context(), "flash", s.T("Data Updated Successfully"))
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
		s.Fail(w, "subscription action", errors.New("billing service not configured"))
		return
	}
	key := "flash"
	if err := do(httpx.PathID(r)); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		slog.Error("subscription action", "path", r.URL.Path, "err", err)
		key, msg = "error", "Action failed"
	}
	s.Sessions.Put(r.Context(), key, s.T(msg))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// canExtend applies the admin_extend setting: who may add free days from the subscriptions list.
func (s *Server) canExtend(r *http.Request) bool {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		return false
	}
	role := adminFrom(r).Role
	switch st["admin_extend"] {
	case "off":
		return false
	case "super":
		return role == "SuperAdmin"
	case "managers":
		return httpx.OneOf(role, "SuperAdmin", "Admin")
	}
	return true // staff (default); the route already limits it to staff roles
}

func (s *Server) subExtend(w http.ResponseWriter, r *http.Request) {
	if !s.canExtend(r) {
		s.deny(w, r, http.StatusForbidden)
		return
	}
	days, err := strconv.Atoi(r.PostFormValue("days"))
	if err != nil || days < 1 || days > 3650 {
		s.Sessions.Put(r.Context(), "error", s.T("Enter a number greater than 0"))
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
