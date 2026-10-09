package web

// Plan CRUD and router sync.

import (
	"database/sql"
	"log/slog"
	"net/http"

	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"strconv"
)

var (
	planNames = []string{"name", "type", "billing", "price", "validity", "validity_unit", "limited", "limit_type",
		"time_limit", "time_unit", "data_limit", "data_unit", "shared_users", "bandwidth_id", "router_id", "pool_id",
		"expired_plan_id", "billing_day", "device", "enabled", "on_login", "on_logout"}
	// the device driver an empty "device" field falls back to, by plan type
	defaultDevices = map[string]string{"Hotspot": "MikrotikHotspot", "PPPoE": "MikrotikPppoe"}
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
	if x.bw, err = s.queries.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 1000}); err != nil {
		return
	}
	if x.rt, err = s.queries.ListRouters(ctx, db.ListRoutersParams{Limit: 1000}); err != nil {
		return
	}
	if x.pools, err = s.queries.ListPools(ctx, db.ListPoolsParams{Limit: 1000}); err != nil {
		return
	}
	x.plans, err = s.queries.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
	return
}

func (f field) none() field { f.Options = append([]option{{"", "-"}}, f.Options...); return f }

func planFields(v, e map[string]string, x planRefs, self int64) []field {
	sel := func(name, label string) field { return text(name, label, v, e).as("select") }
	bw, rt, pool, exp := sel("bandwidth_id", "Bandwidth Name"), sel("router_id", "Router").none(), sel("pool_id", "Pool Name").none(), sel("expired_plan_id", "Expired Plan").none()
	for _, b := range x.bw {
		bw.Options = append(bw.Options, option{fmt.Sprint(b.ID), b.Name})
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
	dev := text("device", "Device", v, e).opts("", "MikrotikHotspot", "MikrotikPppoe", "Dummy", "Radius").hint("Empty = by plan type")
	const notBalance, hotspot = "type !== 'Balance'", "type === 'Hotspot'"
	out := section([]field{
		text("name", "Plan Name", v, e).req(),
		text("type", "Type", v, e).opts("Hotspot", "PPPoE", "Balance").bind(),
		text("billing", "Plan Type", v, e).opts("prepaid", "postpaid").bind(),
		check("enabled", "Enable"),
	}, "Basic", "")
	out = append(out, section([]field{
		text("price", "Plan Price", v, e).as("number").req(),
		text("validity", "Plan Validity", v, e).as("number").show(notBalance),
		text("validity_unit", "Validity Unit", v, e).opts("Mins", "Hrs", "Days", "Months", "Period").show(notBalance),
		text("billing_day", "Billing Day", v, e).as("number").hint("Postpaid: day of month, 1-31").show("billing === 'postpaid'"),
		exp.show(notBalance),
	}, "Price & validity", "")...)
	out = append(out, section([]field{
		check("limited", "Limited").bind(),
		text("limit_type", "Limit Type", v, e).opts("", "Time_Limit", "Data_Limit", "Both_Limit").bind().show("limited"),
		text("time_limit", "Time Limit", v, e).as("number").show("limited && limit_type !== 'Data_Limit'"),
		text("time_unit", "Time Unit", v, e).opts("Mins", "Hrs").show("limited && limit_type !== 'Data_Limit'"),
		text("data_limit", "Data Limit", v, e).as("number").show("limited && limit_type !== 'Time_Limit'"),
		text("data_unit", "Data Unit", v, e).opts("MB", "GB").show("limited && limit_type !== 'Time_Limit'"),
		text("shared_users", "Shared Users", v, e).as("number"),
	}, "Limits", hotspot)...)
	return append(out, section([]field{bw, rt, pool, dev,
		text("on_login", "On Login", v, e).as("textarea").hint("Script run on the router when the customer logs in"),
		text("on_logout", "On Logout", v, e).as("textarea").hint("Script run on the router when the customer logs out")}, "Network & device", notBalance)...)
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
		"expired_plan_id": n(p.ExpiredPlanID), "billing_day": n(p.BillingDay), "device": p.Device, "enabled": fmt.Sprint(p.Enabled), "on_login": p.OnLogin, "on_logout": p.OnLogout}
}

func (s *Server) planList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchPlans(r.Context(), db.SearchPlansParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	routers := map[int64]string{}
	if rs, err := s.routerNames(r); err == nil {
		for _, x := range rs {
			routers[x.ID] = x.Name
		}
	}
	lp := listPage{Heading: "Service Plan", Base: "/admin/plans", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Cols: []string{"Plan Name", "Type", "Plan Price", "Plan Validity", "Router", "Status"}}
	for _, p := range rows {
		st := "Disable"
		if p.Enabled == 1 {
			st = "Enable"
		}
		lp.Rows = append(lp.Rows, listRow{p.ID, []string{p.Name, p.Type + " / " + p.Billing, money(p.Price),
			fmt.Sprint(p.Validity, " ", p.ValidityUnit), routers[p.RouterID.Int64], st}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) planNew(w http.ResponseWriter, r *http.Request) {
	x, err := s.planRefs(r)
	if err != nil {
		s.fail(w, "plan refs", err)
		return
	}
	v := map[string]string{"type": "Hotspot", "billing": "prepaid", "validity": "1", "validity_unit": "Days", "enabled": "1",
		"time_unit": "Hrs", "data_unit": "GB", "shared_users": "1"}
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "load settings", err)
		return
	}
	v["device"] = newPlanDevice(st["default_plan_device"], v["type"])
	s.renderForm(w, r, 200, formPage{"Add Service Plan", "/admin/plans", "/admin/plans", planFields(v, nil, x, 0)})
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

func (s *Server) planEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	p, err := s.queries.GetPlan(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get plan", err)
		return
	}
	x, err := s.planRefs(r)
	if err != nil {
		s.fail(w, "plan refs", err)
		return
	}
	s.renderForm(w, r, 200, formPage{"Edit Service Plan", fmt.Sprint("/admin/plans/", id), "/admin/plans", planFields(planFormValues(p), nil, x, id)})
}

// optInt parses an optional positive integer; empty gives NULL.
func optInt(v string, e map[string]string, key string) sql.NullInt64 {
	if v == "" {
		return sql.NullInt64{}
	}
	n, ok := posInt(v)
	if !ok {
		e[key] = "Enter a number greater than 0"
	}
	return sql.NullInt64{Int64: n, Valid: ok}
}

func optStr(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }

// planSave serves both create (no {id} in the path) and update.
func (s *Server) planSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	ctx := r.Context()
	v := formVals(r, planNames...)
	e := map[string]string{}
	var old db.Plan
	if id != 0 {
		var err error
		if old, err = s.queries.GetPlan(ctx, id); err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.fail(w, "get plan", err)
			return
		}
	}
	if v["name"] == "" {
		e["name"] = "This field is required"
	}
	if !oneOf(v["type"], "Hotspot", "PPPoE", "Balance") {
		e["type"] = "Invalid value"
	}
	if !oneOf(v["billing"], "prepaid", "postpaid") {
		e["billing"] = "Invalid value"
	}
	if !oneOf(v["validity_unit"], "Mins", "Hrs", "Days", "Months", "Period") {
		e["validity_unit"] = "Invalid value"
	}
	if !oneOf(v["device"], "", "MikrotikHotspot", "MikrotikPppoe", "Dummy", "Radius") {
		e["device"] = "Invalid value"
	}
	price, err := strconv.ParseInt(v["price"], 10, 64)
	if err != nil || price < 0 {
		e["price"] = "Enter a number of 0 or more"
	}
	balance := v["type"] == "Balance"
	validity, ok := posInt(v["validity"])
	if !ok {
		if balance && v["validity"] == "" {
			validity, v["validity_unit"] = 1, "Months" // unused for top-ups, the column is NOT NULL
		} else {
			e["validity"] = "Enter a number greater than 0"
		}
	}
	p := db.CreatePlanParams{Name: v["name"], Type: v["type"], Billing: v["billing"], Price: price, Validity: validity,
		ValidityUnit: v["validity_unit"], Device: v["device"], OnLogin: v["on_login"], OnLogout: v["on_logout"]}
	if v["enabled"] == "1" {
		p.Enabled = 1
	}
	p.BillingDay = optInt(v["billing_day"], e, "billing_day")
	if p.BillingDay.Valid && p.BillingDay.Int64 > 31 {
		e["billing_day"] = "Enter a day between 1 and 31"
	}
	if v["billing"] == "postpaid" && !p.BillingDay.Valid {
		e["billing_day"] = "This field is required"
	}
	if !balance {
		if p.BandwidthID = optInt(v["bandwidth_id"], e, "bandwidth_id"); !p.BandwidthID.Valid {
			e["bandwidth_id"] = "This field is required"
		}
		if p.RouterID = optInt(v["router_id"], e, "router_id"); !p.RouterID.Valid && p.Device != "Radius" {
			e["router_id"] = "This field is required" // Radius plans are served by the RADIUS server, no router
		}
		p.PoolID = optInt(v["pool_id"], e, "pool_id")
		p.ExpiredPlanID = optInt(v["expired_plan_id"], e, "expired_plan_id")
		if p.ExpiredPlanID.Valid && p.ExpiredPlanID.Int64 == id {
			e["expired_plan_id"] = "Invalid value"
		}
		p.SharedUsers = optInt(v["shared_users"], e, "shared_users")
		if p.Device == "" {
			p.Device = defaultDevices[p.Type]
		}
		if v["type"] == "Hotspot" && v["limited"] == "1" {
			p.Limited = 1
			p.LimitType = optStr(v["limit_type"])
			if !oneOf(v["limit_type"], "Time_Limit", "Data_Limit", "Both_Limit") {
				e["limit_type"] = "Invalid value"
			}
			if v["limit_type"] != "Data_Limit" {
				if p.TimeLimit = optInt(v["time_limit"], e, "time_limit"); !p.TimeLimit.Valid {
					e["time_limit"] = "This field is required"
				}
				p.TimeUnit = optStr(v["time_unit"])
				if !oneOf(v["time_unit"], "Mins", "Hrs") {
					e["time_unit"] = "Invalid value"
				}
			}
			if v["limit_type"] != "Time_Limit" {
				if p.DataLimit = optInt(v["data_limit"], e, "data_limit"); !p.DataLimit.Valid {
					e["data_limit"] = "This field is required"
				}
				p.DataUnit = optStr(v["data_unit"])
				if !oneOf(v["data_unit"], "MB", "GB") {
					e["data_unit"] = "Invalid value"
				}
			}
		}
	} else {
		p.Device = ""
	}
	if len(e) == 0 {
		var saved db.Plan
		if id == 0 {
			saved, err = s.queries.CreatePlan(ctx, p)
		} else {
			err = s.queries.UpdatePlan(ctx, db.UpdatePlanParams{Name: p.Name, Type: p.Type, Billing: p.Billing, Price: p.Price,
				Validity: p.Validity, ValidityUnit: p.ValidityUnit, TimeLimit: p.TimeLimit, TimeUnit: p.TimeUnit,
				DataLimit: p.DataLimit, DataUnit: p.DataUnit, SharedUsers: p.SharedUsers, BandwidthID: p.BandwidthID,
				RouterID: p.RouterID, PoolID: p.PoolID, Enabled: p.Enabled, Limited: p.Limited, LimitType: p.LimitType,
				ExpiredPlanID: p.ExpiredPlanID, BillingDay: p.BillingDay, OnLogin: p.OnLogin, OnLogout: p.OnLogout,
				Device: p.Device, ID: id})
			if err == nil {
				saved, err = s.queries.GetPlan(ctx, id)
			}
		}
		switch {
		case isUnique(err):
			e["name"] = "Name already exists"
		case isFK(err):
			e["router_id"] = "Invalid value"
		case err != nil:
			s.fail(w, "save plan", err)
			return
		default:
			act, msg, op := "plan.create", "Data Created Successfully", "add"
			if id != 0 {
				act, msg, op = "plan.update", "Data Updated Successfully", "update"
			}
			s.syncPlan(r, op, saved, old.Name)
			s.done(w, r, "/admin/plans", msg, act, v["name"])
			return
		}
	}
	x, rerr := s.planRefs(r)
	if rerr != nil {
		s.fail(w, "plan refs", rerr)
		return
	}
	action, head := "/admin/plans", "Add Service Plan"
	if id != 0 {
		action, head = fmt.Sprint("/admin/plans/", id), "Edit Service Plan"
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/plans", planFields(v, e, x, id)})
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
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Saved, but the router could not be updated")+": "+err.Error())
	}
}

func (s *Server) planDelete(w http.ResponseWriter, r *http.Request) {
	p, _ := s.queries.GetPlan(r.Context(), pathID(r))
	s.remove(w, r, "/admin/plans", "plan", "Plan is in use and cannot be deleted", p.Name, func(id int64) error {
		if err := s.queries.DeletePlan(r.Context(), id); err != nil {
			return err
		}
		s.syncPlan(r, "remove", p, "")
		return nil
	})
}
