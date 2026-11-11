package web

// Plan CRUD and router sync.

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/frand-kod/gobill/internal/service/plan"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"fmt"

	"github.com/frand-kod/gobill/internal/db"
)

var (
	planNames = []string{"name", "type", "billing", "price", "validity", "validity_unit", "limited", "limit_type",
		"time_limit", "time_unit", "data_limit", "data_unit", "shared_users", "bandwidth_id", "router_id", "pool_id",
		"expired_plan_id", "billing_day", "device", "fup_bandwidth_id", "idle_timeout", "login_window", "boost_window", "boost_bandwidth_id", "enabled", "on_login", "on_logout", "audience"}
	defaultDevices = plan.DefaultDevices
)

// planRefs are the rows the plan form selects from.
type planRefs struct {
	bw    []db.Bandwidth
	rt    []db.Router
	pools []db.Pool
	plans []db.Plan
}

func (s *Server) planRefs(r *http.Request) (x planRefs, err error) {
	ctx := r.Context()
	if x.bw, err = s.Queries.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 1000}); err != nil {
		return
	}
	if x.rt, err = s.Queries.ListRouters(ctx, db.ListRoutersParams{Limit: 1000}); err != nil {
		return
	}
	if x.pools, err = s.Queries.ListPools(ctx, db.ListPoolsParams{Limit: 1000}); err != nil {
		return
	}
	if x.plans, err = s.Queries.ListPlans(ctx, db.ListPlansParams{Limit: 1000}); err != nil {
		return
	}
	// the isolir plan and its bandwidth are system rows: no select offers them
	isoPlan, isoBW := s.IsolirIDs(ctx)
	x.bw = slices.DeleteFunc(x.bw, func(b db.Bandwidth) bool { return b.ID == isoBW })
	x.plans = slices.DeleteFunc(x.plans, func(p db.Plan) bool { return p.ID == isoPlan })
	return
}

// scheduleChips are the tap-to-insert examples under the schedule fields.
var scheduleChips = []string{"Mon-Fri 08:00-17:00", "Sat,Sun 09:00-12:00", "* 22:00-06:00", "; "}

func planFields(v, e map[string]string, x planRefs, self int64) []field {
	sel := func(name, label string) field { return text(name, label, v, e).as("select") }
	bw, rt, pool, exp := sel("bandwidth_id", "Bandwidth Name").bind(), sel("router_id", "Router"), sel("pool_id", "Pool Name"), sel("expired_plan_id", "Expired Plan")
	fup := sel("fup_bandwidth_id", "Speed after the data limit")
	fup.Options = []option{{"", "No FUP: reject at the limit"}}
	boost := sel("boost_bandwidth_id", "Boost bandwidth")
	boost.Options = []option{{"", "No boost"}}
	rt.Options, pool.Options, exp.Options = []option{{"", "Choose a router"}}, []option{{"", "No pool"}}, []option{{"", "Default: Isolation, or disconnect"}}
	for _, b := range x.bw {
		bw.Options = append(bw.Options, option{fmt.Sprint(b.ID), b.Name})
	}
	for _, b := range x.bw {
		boost.Options = append(boost.Options, option{fmt.Sprint(b.ID), b.Name})
		fup.Options = append(fup.Options, option{fmt.Sprint(b.ID), b.Name})
	}
	for _, o := range x.rt {
		rt.Options = append(rt.Options, option{fmt.Sprint(o.ID), o.Name})
	}
	for _, p := range x.pools {
		pool.Options = append(pool.Options, option{fmt.Sprint(p.ID), p.Name})
	}
	for _, p := range x.plans {
		if p.ID != self {
			exp.Options = append(exp.Options, option{fmt.Sprint(p.ID), p.Name})
		}
	}
	check := func(name, label string) field {
		f := text(name, label, v, e).as("checkbox")
		f.Checked = v[name] == "1"
		return f
	}
	dev := text("device", "Device", v, e).as("select").bind()
	dev.Options = []option{{"Radius", "RADIUS (gobill)"}, {"", "MikroTik API (by plan type)"}, {"MikrotikHotspot", "MikroTik API (Hotspot driver)"},
		{"MikrotikPppoe", "MikroTik API (PPPoE driver)"}, {"Dummy", "Dummy"}}
	dev.Hint = "RADIUS (gobill) needs no router. MikroTik API and Dummy need one."
	aud := text("audience", "Who can use this plan", v, e).as("select")
	aud.Options = []option{{"public", "Public — sold on the customer portal"}, {"admin", "Admin only — only an admin can give it"}}
	const notBalance, hotspot, pppoe, radius = "type !== 'Balance'", "type === 'Hotspot'", "type === 'PPPoE'", "device !== 'Radius'"
	validity := row("validity", "Validity",
		text("validity", "Plan Validity", v, e).as("number").bind(),
		text("validity_unit", "Validity Unit", v, e).opts("Mins", "Hrs", "Days", "Months", "Period").bind().class("w-40 flex-none")).show(notBalance)
	validity.Error = e["validity"]
	out := section([]field{
		text("name", "Plan Name", v, e).req().wide(),
		text("type", "Service", v, e).opts("Hotspot", "PPPoE", "Balance").bind(),
		text("billing", "Billing (Prepaid/Postpaid)", v, e).opts("prepaid", "postpaid").bind(),
		bw.show(notBalance).reqIf(notBalance),
		row("availability", "Availability", check("enabled", "Enable"), aud).wide().
			hint("Enable turns the plan on or off. Public: sold on the customer portal. Admin only: hidden from the portal, so customers cannot buy, extend or auto-renew it, but an admin can still give it."),
	}, "Basic", "")
	out = append(out, section([]field{
		text("price", "Plan Price", v, e).as("number").req().bind(),
		validity,
		text("billing_day", "Billing Day", v, e).as("number").hint("Day of month, 1-31. Required for postpaid plans. Empty on a Period plan: day 20").reqIf("billing === 'postpaid'").show("billing === 'postpaid' || validity_unit === 'Period'"),
	}, "Price & validity", "")...)
	out = append(out, section([]field{
		exp.show(notBalance).hint("Default: a RADIUS customer is isolated when isolation is enabled, otherwise disconnected. Pick a plan to send them there instead."),
	}, "When the plan ends", notBalance)...)
	out = append(out, section([]field{
		check("limited", "Limited").bind(),
		text("limit_type", "Limit Type", v, e).opts("", "Time_Limit", "Data_Limit", "Both_Limit").bind().show("limited").
			hint("PPPoE plans on RADIUS can only use Data_Limit."),
		text("time_limit", "Time Limit", v, e).as("number").show("limited && limit_type !== 'Data_Limit'"),
		text("time_unit", "Time Unit", v, e).opts("Mins", "Hrs").show("limited && limit_type !== 'Data_Limit'"),
		text("data_limit", "Data Limit", v, e).as("number").show("limited && limit_type !== 'Time_Limit'"),
		text("data_unit", "Data Unit", v, e).opts("MB", "GB").show("limited && limit_type !== 'Time_Limit'"),
		fup.show("limited && limit_type !== 'Time_Limit' && device === 'Radius'").
			hint("FUP (fair usage): past the data limit the customer stays online at this speed until the plan ends, instead of being rejected. RADIUS (gobill) only."),
		text("idle_timeout", "Idle timeout (seconds)", v, e).as("number").show("device === 'Radius'").
			hint("The NAS drops a session that sends no traffic for this long. Empty = not sent. RADIUS (gobill) only."),
		text("shared_users", "Device limit", v, e).as("number").bind().show(hotspot).
			hint("How many devices can be online with this plan at once. When the limit is full, Settings > Customers & Billing decides: kick the oldest device or reject the new login."),
	}, "Limits", hotspot+" || "+pppoe+" && device === 'Radius'")...)
	out = append(out, section([]field{
		dev.show(notBalance),
		rt.show(radius).reqIf(radius),
		pool.show(pppoe),
	}, "Network & device", notBalance)...)
	out = append(out, section([]field{
		text("login_window", "Login hours", v, e).hint("When customers of this plan may log in, in the app time zone. Day names are English: Mon, Tue, Wed, Thu, Fri, Sat, Sun. Separate rules with ;. A window past midnight is fine: 22:00-06:00. Empty = always. The session is cut at the end of the window. RADIUS (gobill) only.").chips(scheduleChips...).wide(),
		boost.hint("Speed used during the boost hours instead of the plan's own bandwidth. A customer past the data limit still gets the FUP speed. RADIUS (gobill) only."),
		text("boost_window", "Boost hours", v, e).hint("Same format as Login hours, e.g. * 00:00-06:00 for a night boost. Online customers are switched within a minute.").chips(scheduleChips...),
	}, "Schedule", "device === 'Radius'")...)
	scripts := section([]field{
		text("on_login", "On Login", v, e).as("textarea"),
		text("on_logout", "On Logout", v, e).as("textarea")}, "Router scripts", notBalance)
	for i := range scripts {
		scripts[i].Fold = true
	}
	return append(out, scripts...)
}

// planHelp is the plan form's extras: the docs link, a live summary and a sticky save bar. The
// summary is an Alpine expression over the bound fields; its word comes from the catalog.
func (s *Server) planHelp() formHelp {
	word := jsonStr(s.T("device"))
	sum := `(type === 'Balance' ? [type] : [type,
		bandwidth_id && $root.bandwidth_id.selectedOptions[0]?.text,
		validity && validity_unit && validity + ' ' + $root.validity_unit.selectedOptions[0]?.text,
		type === 'Hotspot' && shared_users && shared_users + ' ' + ` + word + `])
		.concat('Rp' + (Number(price) || 0).toLocaleString('id-ID')).filter(Boolean).join(' · ')`
	return formHelp{Docs: "/admin/docs/configuration", Summary: sum, Narrow: true, Sticky: true}
}

// renderPlanForm renders the plan form page with its extras.
func (s *Server) renderPlanForm(w http.ResponseWriter, r *http.Request, status int, head, action string, fs []field) {
	s.Render(w, r, status, "form", Page{Title: head, Data: formPage{head, action, "/admin/plans", fs}, Help: s.planHelp()})
}

func planFormValues(p db.Plan) map[string]string {
	n := func(x sql.NullInt64) string {
		if x.Valid {
			return fmt.Sprint(x.Int64)
		}
		return ""
	}
	return map[string]string{"name": p.Name, "type": p.Type, "billing": p.Billing, "price": fmt.Sprint(p.Price),
		"validity": fmt.Sprint(p.Validity), "validity_unit": p.ValidityUnit, "limited": fmt.Sprint(p.Limited),
		"limit_type": p.LimitType.String, "time_limit": n(p.TimeLimit), "time_unit": p.TimeUnit.String,
		"data_limit": n(p.DataLimit), "data_unit": p.DataUnit.String, "shared_users": n(p.SharedUsers),
		"bandwidth_id": n(p.BandwidthID), "router_id": n(p.RouterID), "pool_id": n(p.PoolID),
		"expired_plan_id": n(p.ExpiredPlanID), "billing_day": n(p.BillingDay), "device": p.Device, "fup_bandwidth_id": n(p.FupBandwidthID), "idle_timeout": n(p.IdleTimeout), "login_window": p.LoginWindow, "boost_window": p.BoostWindow, "boost_bandwidth_id": n(p.BoostBandwidthID), "enabled": fmt.Sprint(p.Enabled), "on_login": p.OnLogin, "on_logout": p.OnLogout, "audience": p.Audience}
}

func (s *Server) planList(w http.ResponseWriter, r *http.Request) {
	q, pg := view.Paging(r)
	isoPlan, _ := s.IsolirIDs(r.Context())
	rows, err := s.Queries.SearchPlans(r.Context(), db.SearchPlansParams{Q: q, PageLimit: pg.Limit(), PageOffset: pg.Offset(), HiddenID: isoPlan})
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	routers := map[int64]string{}
	if rs, err := s.routerNames(r); err == nil {
		for _, x := range rs {
			routers[x.ID] = x.Name
		}
	}
	lp := listPage{Heading: "Service Plan", Base: "/admin/plans", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Cols: []string{"Plan Name", "Type", "Plan Price", "Plan Validity", "Router", "Audience", "Features", "Status"}}
	if isoPlan != 0 {
		lp.Note, lp.NoteLink = "The Isolir plan is managed in Settings > Isolir", "/admin/settings/isolir"
	}
	for _, p := range rows {
		st, aud := "Disable", ""
		if p.Enabled == 1 {
			st = "Enable"
		}
		if p.Audience == "admin" {
			aud = "Admin only"
		}
		var feats []string // schedule, idle timeout and FUP: shown as badges
		if p.LoginWindow != "" || p.BoostWindow != "" {
			feats = append(feats, "Schedule")
		}
		if p.IdleTimeout.Int64 > 0 {
			feats = append(feats, "Idle timeout")
		}
		if p.FupBandwidthID.Valid {
			feats = append(feats, "FUP")
		}
		lp.Rows = append(lp.Rows, listRow{p.ID, []string{p.Name, p.Type + " / " + p.Billing, view.Money(p.Price),
			fmt.Sprint(p.Validity, " ", p.ValidityUnit), routers[p.RouterID.Int64], aud, strings.Join(feats, ", "), st}})
	}
	total, err := s.Queries.CountPlans(r.Context(), db.CountPlansParams{Term: q, HiddenID: isoPlan})
	if err != nil {
		s.Fail(w, "count plans", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) planNew(w http.ResponseWriter, r *http.Request) {
	x, err := s.planRefs(r)
	if err != nil {
		s.Fail(w, "plan refs", err)
		return
	}
	v := map[string]string{"type": "Hotspot", "billing": "prepaid", "validity": "1", "validity_unit": "Days", "enabled": "1",
		"time_unit": "Hrs", "data_unit": "GB", "shared_users": "1", "audience": "public"}
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "load settings", err)
		return
	}
	v["device"] = newPlanDevice(st["default_plan_device"], v["type"])
	s.renderPlanForm(w, r, 200, "Add Service Plan", "/admin/plans", planFields(v, nil, x, 0))
}

// newPlanDevice is the device the new plan form preselects. A Mikrotik driver that does not match
// the plan type gives the type's own driver; Balance plans have no device.
func newPlanDevice(setting, typ string) string {
	if typ == "Balance" {
		return ""
	}
	if (setting == "MikrotikHotspot" || setting == "MikrotikPppoe") && setting != defaultDevices[typ] {
		return defaultDevices[typ]
	}
	return setting
}

// isolirManaged flashes an error and reports true when id is the isolir plan, which is edited only in Settings > Isolir.
func (s *Server) isolirManaged(w http.ResponseWriter, r *http.Request, id int64) bool {
	if iso, _ := s.IsolirIDs(r.Context()); iso == 0 || iso != id {
		return false
	}
	s.Sessions.Put(r.Context(), "error", s.T("The Isolir plan is managed in Settings > Isolir"))
	http.Redirect(w, r, "/admin/plans", http.StatusSeeOther)
	return true
}

func (s *Server) planEdit(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	if s.isolirManaged(w, r, id) {
		return
	}
	p, err := s.Queries.GetPlan(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get plan", err)
		return
	}
	x, err := s.planRefs(r)
	if err != nil {
		s.Fail(w, "plan refs", err)
		return
	}
	s.renderPlanForm(w, r, 200, "Edit Service Plan", fmt.Sprint("/admin/plans/", id), planFields(planFormValues(p), nil, x, id))
}

// planSave serves both create (no {id} in the path) and update.
func (s *Server) planSave(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	if s.isolirManaged(w, r, id) {
		return
	}
	v := httpx.FormVals(r, planNames...)
	e := map[string]string{}
	res, err := s.Plans.Save(r.Context(), &plan.SaveInput{ID: id, V: v, Errs: e})
	if errors.Is(err, plan.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "save plan", err)
		return
	}
	if res.Saved {
		act, msg, op := "plan.create", "Data Created Successfully", "add"
		if id != 0 {
			act, msg, op = "plan.update", "Data Updated Successfully", "update"
		}
		s.syncPlan(r, op, res.Plan, res.Old.Name)
		s.done(w, r, "/admin/plans", msg, act, v["name"])
		return
	}
	x, rerr := s.planRefs(r)
	if rerr != nil {
		s.Fail(w, "plan refs", rerr)
		return
	}
	action, head := "/admin/plans", "Add Service Plan"
	if id != 0 {
		action, head = fmt.Sprint("/admin/plans/", id), "Edit Service Plan"
	}
	s.renderPlanForm(w, r, http.StatusUnprocessableEntity, head, action, planFields(v, e, x, id))
}

// syncPlan tells the plan's router about a change. The DB change stays if the router fails;
// the admin gets a warning and the failure is logged.
func (s *Server) syncPlan(r *http.Request, op string, p db.Plan, oldName string) {
	if s.Billing == nil {
		return
	}
	if err := s.Billing.SyncPlan(r.Context(), op, p, oldName); err != nil {
		slog.Error("sync plan", "op", op, "plan", p.Name, "err", err)
		s.logActivity(r, "plan.sync_failed", fmt.Sprintf("%s %s: %v", op, p.Name, err))
		s.Sessions.Put(r.Context(), "error", s.T("Saved, but the router could not be updated")+": "+err.Error())
	}
}

func (s *Server) planDelete(w http.ResponseWriter, r *http.Request) {
	if s.isolirManaged(w, r, httpx.PathID(r)) {
		return
	}
	p, _ := s.Queries.GetPlan(r.Context(), httpx.PathID(r))
	s.remove(w, r, "/admin/plans", "plan", "Plan is in use and cannot be deleted", p.Name, func(id int64) error {
		if err := s.Queries.DeletePlan(r.Context(), id); err != nil {
			return err
		}
		s.syncPlan(r, "remove", p, "")
		return nil
	})
}
