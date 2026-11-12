package web

// Customer list, detail, create, edit, save and delete.

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/service/customer"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/service/diagnose"
)

var (
	custStatuses = []string{"Active", "Banned", "Disabled", "Inactive", "Limited", "Suspended"}
	custNames    = []string{"username", "fullname", "address", "phone", "email", "service_type", "pppoe_username",
		"pppoe_ip", "billing_day", "auto_renewal", "status", "coordinates", "bind_mac", "bind_port_id", "send_welcome_message", "notify_sms", "notify_wa", "notify_email", "plan"}
)

func custFields(v, e map[string]string, editing bool) []field {
	pw := text("password", "Password", v, e).as("password")
	pw.Gen = true
	sec := text("secret", "Router Secret", v, e).as("password").hint("Hotspot/PPPoE password on the router. Leave empty to use the Password above (or keep the current one when no new password is entered).")
	ar := text("auto_renewal", "Auto Renewal", v, e).as("checkbox")
	ar.Checked = v["auto_renewal"] == "1"
	fs := []field{text("username", "Username", v, e).req()}
	if editing {
		pw.Hint = "Leave empty to keep the current password"
	} else {
		pw.Required = true
	}
	acct := section(append(fs, pw, text("fullname", "Full Name", v, e).req(), text("status", "Status", v, e).opts(custStatuses...)), "Account", "")
	contact := section([]field{
		text("phone", "Phone Number", v, e).as("tel"),
		text("email", "Email", v, e).as("email"),
		text("address", "Address", v, e),
		text("coordinates", "Coordinates", v, e).as("coords").hint("lat,lng, e.g. -6.2,106.8. Optional; shown on the customer map."),
	}, "Contact", "")
	svc := section([]field{
		text("service_type", "Service Type", v, e).opts("Hotspot", "PPPoE", "Others"),
		text("pppoe_username", "PPPoE Username", v, e),
		text("pppoe_ip", "PPPoE IP", v, e),
		sec,
		text("bind_mac", "Lock to MAC address", v, e).hint("RADIUS only. Optional: the customer can log in only from this device (Calling-Station-Id). Empty = any device."),
		text("bind_port_id", "Lock to port (NAS-Port-Id)", v, e).hint("RADIUS only. Optional: the customer can log in only on this NAS port, e.g. ether3 or the PPPoE interface. Empty = any port."),
		text("billing_day", "Billing Day", v, e).as("number").hint("Day of month, 1-31. Optional; overrides the plan."),
		ar,
	}, "Service & billing", "")
	out := append(append(acct, contact...), svc...)
	if !editing { // old form: Send Welcome Message + sms / wa / mail
		chk := func(n, l string) field { f := text(n, l, v, e).as("checkbox"); f.Checked = v[n] == "1"; return f }
		out = append(out, section([]field{chk("send_welcome_message", "Send Welcome Message"), chk("notify_sms", "SMS"),
			chk("notify_wa", "WhatsApp"), chk("notify_email", "Email")}, "Welcome Message", "")...)
	}
	return out
}

// canRecharge: the roles that may recharge a customer (same list as the customer page's CanSell).
func canRecharge(role string) bool { return httpx.OneOf(role, "SuperAdmin", "Admin", "Agent", "Sales") }

// addPlans are the plans the add form offers: the Recharge page's list (SuperAdmin/Admin also see
// disabled plans), without Balance plans, and only those that fit the service type. Empty for roles
// that may not recharge.
func (s *Server) addPlans(r *http.Request, serviceType string) ([]option, map[int64]db.Plan, error) {
	fit := map[int64]db.Plan{}
	if !canRecharge(adminFrom(r).Role) {
		return nil, fit, nil
	}
	opts, byID, err := s.planOptions(r, !httpx.OneOf(adminFrom(r).Role, "SuperAdmin", "Admin"))
	if err != nil {
		return nil, nil, err
	}
	var out []option
	for _, o := range opts {
		id, _ := strconv.ParseInt(o.Value, 10, 64)
		if p := byID[id]; p.Type != "Balance" && (serviceType == "Others" || p.Type == serviceType) {
			out = append(out, o)
			fit[id] = p
		}
	}
	return out, fit, nil
}

// addCustomerForm is the add form: the customer fields plus the optional "Activate the plan now" card.
func (s *Server) addCustomerForm(r *http.Request, v, e map[string]string) ([]field, error) {
	fs, err := s.customerForm(r.Context(), 0, v, e, false)
	if err != nil {
		return nil, err
	}
	opts, _, err := s.addPlans(r, v["service_type"])
	if err != nil || !canRecharge(adminFrom(r).Role) {
		return fs, err
	}
	plan := text("plan", "Plan", v, e).as("select")
	plan.Options = append([]option{{"", "-"}}, opts...)
	pay := field{Name: "payment_text", Label: "Payment method: Cash", Type: "note"}
	return append(fs, section([]field{plan, pay}, "Activate the plan now", "")...), nil
}

var custSorts = []string{"username", "fullname", "balance", "status", "expires"}

// endsCell is the "Ends On" cell: the nearest active expiry, "—" when there is none, and "Today"
// (a badge in list.html) when it ends on the current day.
func (s *Server) endsCell(unix int64) string {
	if unix == 0 {
		return "—"
	}
	loc := s.Location()
	if time.Unix(unix, 0).In(loc).Format("2006-01-02") == time.Now().In(loc).Format("2006-01-02") {
		return "Today"
	}
	return s.TS(unix)
}

// custQuery reads the list filters and sort; unknown values fall back to "any" / newest first.
func custQuery(r *http.Request) (db.FilterCustomersParams, string, string) {
	g := r.URL.Query().Get
	p := db.FilterCustomersParams{Q: strings.TrimSpace(g("q")), ServiceType: g("service_type"), Status: g("status")}
	if !httpx.OneOf(p.ServiceType, "Hotspot", "PPPoE", "Others") {
		p.ServiceType = ""
	}
	if !httpx.OneOf(p.Status, custStatuses...) {
		p.Status = ""
	}
	sort, dir := g("sort"), "asc"
	if !httpx.OneOf(sort, custSorts...) {
		sort = ""
	} else if g("dir") == "desc" {
		dir = "desc"
	}
	if sort != "" {
		p.Sort = sort + "_" + dir
	}
	return p, sort, dir
}

func anyOpts(label string, o ...string) []option {
	out := []option{{"", label}}
	for _, x := range o {
		out = append(out, option{x, x})
	}
	return out
}

// phoneTerm turns a typed national number (0812...) into the stored form (62812...), so the customer
// search finds it; other text is searched as typed. ponytail: a digit-only username starting with 0 no
// longer matches its own name in this search.
func phoneTerm(q string) string {
	if len(q) >= 4 && strings.HasPrefix(q, "0") && strings.Trim(q, "0123456789") == "" {
		return db.CanonicalPhone(q)
	}
	return q
}

// filterCustomers is FilterCustomers, through FilterCustomersNewest for the default view (no search,
// filter or sort), which reads the page off the id index instead of sorting every customer.
func (s *Server) filterCustomers(ctx context.Context, p db.FilterCustomersParams) ([]db.FilterCustomersRow, error) {
	if p != (db.FilterCustomersParams{PageOffset: p.PageOffset, PageLimit: p.PageLimit}) {
		return s.Queries.FilterCustomers(ctx, p)
	}
	rows, err := s.Queries.FilterCustomersNewest(ctx, db.FilterCustomersNewestParams{PageOffset: p.PageOffset, PageLimit: p.PageLimit})
	out := make([]db.FilterCustomersRow, len(rows))
	for i, r := range rows {
		out[i] = db.FilterCustomersRow(r)
	}
	return out, err
}

func (s *Server) custList(w http.ResponseWriter, r *http.Request) {
	p, sort, dir := custQuery(r)
	_, pg := view.Paging(r)
	p.PageLimit, p.PageOffset = pg.Limit(), pg.Offset()
	typed := p.Q
	p.Q = phoneTerm(p.Q)
	rows, err := s.filterCustomers(r.Context(), p)
	if err != nil {
		s.Fail(w, "list customers", err)
		return
	}
	role := adminFrom(r).Role
	lp := listPage{Heading: "Customer", Base: "/admin/customers", Q: typed, Searchable: true, ViewLink: true,
		DeleteNote: "The balance and active plans are removed (plans are also removed from the router). Transactions are kept.",
		CanCreate:  httpx.OneOf(role, "SuperAdmin", "Admin", "Agent", "Sales"), CanEdit: httpx.OneOf(role, "SuperAdmin", "Admin"),
		Cols:     []string{"Username", "Full Name", "Balance", "Package", "Service Type", "PPPoE Username", "Ends On", "Status"},
		SortKeys: []string{"username", "fullname", "balance", "", "", "", "expires", "status"}, Sort: sort, Dir: dir,
		Filters: []filter{{"service_type", p.ServiceType, anyOpts("Service Type", "Hotspot", "PPPoE", "Others"), "Service Type"},
			{"status", p.Status, anyOpts("Status", custStatuses...), "Status"}}}
	if httpx.OneOf(role, "SuperAdmin", "Admin", "Agent", "Sales") {
		lp.Bulk = []bulkAction{{"message", "Send message to selected", false, false, false},
			{"recharge", "Recharge same plan", false, false, true}}
	}
	for _, c := range rows {
		lp.Rows = append(lp.Rows, listRow{c.ID, []string{c.Username, c.Fullname, view.Money(c.Balance), c.Packages, c.ServiceType, c.PppoeUsername, s.endsCell(c.NextExpires), c.Status}})
	}
	lp.Links = []option{{"/admin/customers/export?" + lp.query().Encode(), "Export CSV"}}
	var total int64
	if p.Q == "" && p.ServiceType == "" && p.Status == "" {
		total, err = s.Queries.CountCustomers(r.Context()) // no LIKE scan over every customer
	} else {
		total, err = s.Queries.CountFilteredCustomers(r.Context(), db.CountFilteredCustomersParams{Q: p.Q, ServiceType: p.ServiceType, Status: p.Status})
	}
	if err != nil {
		s.Fail(w, "count customers", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

// custPick is the customer picker's suggestions, as JSON. Non-empty q matches username,
// full name, phone or PPPoE username; empty q lists the first customers (Active first). limit is capped at
// 20 (default 8 for a search, 20 for the empty list). Staff only (route).
func (s *Server) custPick(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := int64(8)
	if q == "" {
		limit = 20
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = int64(min(n, 20))
	}
	if len(q) <= 100 {
		rows, err := s.Queries.QuickSearchCustomers(r.Context(), db.QuickSearchCustomersParams{Term: q, Limit: limit})
		if err != nil {
			s.Fail(w, "search customers", err)
			return
		}
		for _, c := range rows {
			out = append(out, map[string]any{"id": c.ID, "username": c.Username, "fullname": c.Fullname, "phone": c.Phone,
				"pppoe_username": c.PppoeUsername, "status": c.Status, "balance": view.Money(c.Balance),
				"url": fmt.Sprint("/admin/customers/", c.ID)})
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

// custExport streams the current filter (all pages) as CSV, like the old customers csv.
func (s *Server) custExport(w http.ResponseWriter, r *http.Request) {
	p, _, _ := custQuery(r)
	p.PageLimit = -1
	rows, err := s.filterCustomers(r.Context(), p)
	if err != nil {
		s.Fail(w, "export customers", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="customers-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Username", "Full Name", "Phone Number", "Email", "Balance", "Package", "Service Type", "PPPoE Username", "Status", "Created"})
	for _, c := range rows {
		rec := []string{c.Username, c.Fullname, c.Phone, c.Email, fmt.Sprint(c.Balance), c.Packages, c.ServiceType, c.PppoeUsername, c.Status, s.TS(c.CreatedAt)}
		httpx.CSVSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}

func (s *Server) custNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"service_type": "Others", "status": "Active", "auto_renewal": "1"}
	fs, err := s.addCustomerForm(r, v, nil)
	if err != nil {
		s.Fail(w, "custom fields", err)
		return
	}
	s.renderForm(w, r, 200, formPage{"Add New Contact", "/admin/customers", "/admin/customers", fs})
}

func (s *Server) custGet(w http.ResponseWriter, r *http.Request) (db.Customer, bool) {
	c, err := s.Queries.GetCustomer(r.Context(), httpx.PathID(r))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return c, false
	} else if err != nil {
		s.Fail(w, "get customer", err)
		return c, false
	}
	return c, true
}

func (s *Server) custEdit(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	v := map[string]string{"username": c.Username, "fullname": c.Fullname, "address": c.Address, "phone": c.Phone, "email": c.Email, "coordinates": c.Coordinates,
		"service_type": c.ServiceType, "pppoe_username": c.PppoeUsername, "pppoe_ip": c.PppoeIp,
		"auto_renewal": fmt.Sprint(c.AutoRenewal), "status": c.Status}
	if c.BillingDay.Valid {
		v["billing_day"] = fmt.Sprint(c.BillingDay.Int64)
	}
	if b, err := s.Queries.GetCustomerBinding(r.Context(), c.ID); err == nil {
		v["bind_mac"], v["bind_port_id"] = b.Mac, b.PortID
	}
	fs, err := s.customerForm(r.Context(), c.ID, v, nil, true)
	if err != nil {
		s.Fail(w, "custom fields", err)
		return
	}
	s.renderForm(w, r, 200, formPage{"Edit Contact: " + c.Username, fmt.Sprint("/admin/customers/", c.ID), "/admin/customers", fs})
}

type subRow struct{ Plan, Router, Expires, Status string }

type custDetail struct {
	Manage       bool // SuperAdmin/Admin: deactivate, sync, login as customer
	C            db.Customer
	Created      string
	SecretSet    bool
	CanEdit      bool
	CanSell      bool // may recharge
	Plans        []option
	Methods      []option // payment methods offered on the recharge form
	Subs         []subRow
	Usage        *core.RadiusUsage
	Trx          []db.Transaction
	Custom       []field // custom fields with this customer's values
	Online       string  // "" unless check_customer_online is yes; else Online, Offline or Error
	Iso          string  // end of the running isolation, "" when none
	Diag         diagnose.Diagnosis
	RouterFailed bool // a delete was refused because the router could not remove the customer: offer "Delete anyway"
}

func (s *Server) custView(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	role := adminFrom(r).Role
	d := custDetail{Manage: httpx.OneOf(role, "SuperAdmin", "Admin"), C: c, Created: s.TS(c.CreatedAt), SecretSet: len(c.SecretEnc) > 0,
		CanEdit: httpx.OneOf(role, "SuperAdmin", "Admin"), CanSell: httpx.OneOf(role, "SuperAdmin", "Admin", "Agent", "Sales"), RouterFailed: r.URL.Query().Get("router_failed") == "1"}
	opts, plans, err := s.planOptions(r, !d.Manage) // PHP: SuperAdmin/Admin may recharge a disabled plan
	if err != nil {
		s.Fail(w, "list plans", err)
		return
	}
	d.Plans = opts
	d.Methods = s.rechargeMethods(ctx)
	subs, err := s.Queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
	if err != nil {
		s.Fail(w, "list subscriptions", err)
		return
	}
	for _, x := range subs {
		d.Subs = append(d.Subs, subRow{plans[x.PlanID].Name, x.Type, s.SubExpiry(x.PendingStart, x.ExpiresAt), x.Status})
	}
	if d.Trx, err = s.Queries.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 10}); err != nil {
		s.Fail(w, "list transactions", err)
		return
	}
	if d.Usage, err = s.RadiusUsage(ctx, c); err != nil {
		s.Fail(w, "customer usage", err)
		return
	}
	if d.Custom, err = s.cfFormFields(ctx, c.ID, map[string]string{}, nil); err != nil {
		s.Fail(w, "custom fields", err)
		return
	}
	if iso, err := s.Queries.GetRunningIsolation(ctx, db.GetRunningIsolationParams{CustomerID: c.ID, Now: time.Now().Unix()}); err == nil {
		d.Iso = s.TS(iso.EndsAt)
	}
	d.Online = s.customerOnline(ctx, c, subs)
	d.Diag = s.diagnose(ctx, c, subs, d.Online)
	s.Render(w, r, 200, "customer", Page{
		Title: c.Username,
		Flash: s.Sessions.PopString(ctx, "flash"),
		Error: s.Sessions.PopString(ctx, "error"),
		Data:  d,
	})
}

// customerOnline asks the device of the first active plan (old check_customer_online). Device
// errors show as Error, as the old red dot did.
func (s *Server) customerOnline(ctx context.Context, c db.Customer, subs []db.Subscription) string {
	st, err := s.LoadSettings(ctx)
	if err != nil || s.Billing == nil || st["check_customer_online"] != "yes" {
		return ""
	}
	for _, x := range subs {
		if x.Status != "active" {
			continue
		}
		p, err := s.Queries.GetPlan(ctx, x.PlanID)
		if err != nil {
			return "Error"
		}
		on, err := s.Billing.CustomerOnline(ctx, c, p)
		switch {
		case err != nil:
			return "Error"
		case on:
			return "Online"
		}
		return "Offline"
	}
	return ""
}

// setPassword stores a new portal password and, as in the old app (one password for portal and
// router), the same value as the router secret.
func (s *Server) setPassword(ctx context.Context, id int64, pass string) error {
	return s.CustomerSvc().SetPassword(ctx, id, pass)
}

// custSave serves both create (no {id} in the path) and update.
func (s *Server) custSave(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	v := httpx.FormVals(r, custNames...)
	pass, sec := r.PostFormValue("password"), r.PostFormValue("secret")
	e := map[string]string{}
	var cur db.Customer
	if id != 0 {
		var ok bool
		if cur, ok = s.custGet(w, r); !ok {
			return
		}
	} else if pass == "" {
		e["password"] = "This field is required"
	}
	v["username"] = strings.TrimSpace(v["username"])
	if id != 0 && !r.PostForm.Has("username") { // a post without the field keeps the username
		v["username"] = cur.Username
	}
	if pass != "" {
		st, err := s.LoadSettings(r.Context())
		if err != nil {
			s.Fail(w, "customer password", err)
			return
		}
		if msg := s.CustomerPasswordErr(st, v["username"], pass); msg != "" {
			e["password"] = msg
		}
	}
	cfv, err := s.cfCheck(r.Context(), r, v, e)
	if err != nil {
		s.Fail(w, "check custom fields", err)
		return
	}
	// Optional plan on the add form, activated with Cash after the customer is created.
	var plan db.Plan
	if id == 0 && v["plan"] != "" {
		_, fit, perr := s.addPlans(r, v["service_type"])
		if perr != nil {
			s.Fail(w, "list plans", perr)
			return
		}
		pid, _ := httpx.PosInt(v["plan"])
		if plan = fit[pid]; plan.ID == 0 {
			e["plan"] = msgPlanMissing
		}
	}
	ctx := r.Context()
	res, err := s.CustomerSvc().Save(ctx, &customer.SaveInput{ID: id, Cur: cur, V: v, Pass: pass, Secret: sec,
		AdminID: adminFrom(r).ID, CF: cfv, Errs: e})
	if err != nil {
		s.Fail(w, "save customer", err)
		return
	}
	cid, created := res.ID, res.Created
	if res.Saved {
		act, msg := "customer.create", "Data Created Successfully"
		if id == 0 && v["send_welcome_message"] == "1" { // after the commit: no welcome for a rolled-back customer
			s.welcome(ctx, created, welcomeChannels(v), pass)
		}
		if id != 0 {
			act, msg = "customer.update", "Data Updated Successfully"
			// A new password, username, pppoe username/ip goes to the router, as the old app did.
			if v["status"] != cur.Status && s.Billing != nil { // leaving Active cuts the router; coming back re-syncs
				var serr error
				if cur.Status == "Active" {
					_, serr = s.Billing.SuspendCustomer(r.Context(), id)
				} else if v["status"] == "Active" {
					_, serr = s.Billing.SyncCustomer(r.Context(), id, adminFrom(r).ID)
				}
				if serr != nil {
					slog.Error("customer status router update", "customer", v["username"], "err", serr)
					s.logActivity(r, "customer.sync_failed", fmt.Sprintf("%s: %v", v["username"], serr))
					s.Sessions.Put(r.Context(), "error", s.T("Saved, but the router could not be updated")+": "+serr.Error())
				}
			}
			if v["status"] == "Active" && cur.Status == "Active" && (pass != "" || sec != "" || v["username"] != cur.Username || v["pppoe_username"] != cur.PppoeUsername || v["pppoe_ip"] != cur.PppoeIp) && s.Billing != nil {
				if _, serr := s.Billing.SyncAfterEdit(r.Context(), cur, adminFrom(r).ID); serr != nil {
					slog.Error("sync customer", "customer", v["username"], "err", serr)
					s.logActivity(r, "customer.sync_failed", fmt.Sprintf("%s: %v", v["username"], serr))
					s.Sessions.Put(r.Context(), "error", s.T("Saved, but the router could not be updated")+": "+serr.Error())
				}
			}
		}
		next := "/admin/customers"
		if id == 0 { // a new customer has no plan yet: go to the page where one is chosen
			next, msg = fmt.Sprint("/admin/customers/", cid), "Customer created. Choose a plan below to switch the internet on."
			if plan.ID != 0 {
				if msg = s.activateNew(w, r, created, plan, act); msg == "" {
					return
				}
			}
		}
		s.done(w, r, next, msg, act, v["username"])
		return
	}
	action, head := "/admin/customers", "Add New Contact"
	if id != 0 {
		action, head = fmt.Sprint("/admin/customers/", id), "Edit Contact: "+cur.Username
	}
	fs, err := s.customerForm(r.Context(), id, v, e, id != 0)
	if id == 0 && err == nil {
		fs, err = s.addCustomerForm(r, v, e)
	}
	if err != nil {
		s.Fail(w, "custom fields", err)
		return
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/customers", fs})
}

// activateNew charges the plan chosen on the add form with Cash, right after the customer was
// created. It returns the flash for the customer page, or "" after it has redirected with the
// activation error (the customer stays, without a plan).
func (s *Server) activateNew(w http.ResponseWriter, r *http.Request, c db.Customer, plan db.Plan, act string) string {
	ctx := r.Context()
	devFailed, err := s.chargePlan(r, c, plan, "Cash", "Cash")
	if err != nil {
		s.logActivity(r, act, c.Username)
		s.Sessions.Put(ctx, "error", s.T("Customer created, but the plan could not be activated")+": "+s.T(rechargeFailMsg(err, c, plan)))
		http.Redirect(w, r, fmt.Sprint("/admin/customers/", c.ID), http.StatusSeeOther)
		return ""
	}
	s.logActivity(r, "customer.recharge", c.Username+" ["+plan.Name+"]")
	if devFailed {
		s.Sessions.Put(ctx, "warn", s.T(msgNotSynced))
	}
	return "Customer created and plan activated, paid in Cash"
}

func (s *Server) custDelete(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	c, err := s.Queries.GetCustomer(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if s.Billing == nil {
		s.Fail(w, "delete customer", errors.New("billing service not configured"))
		return
	}
	// Transactions are kept (customer_id -> NULL). The router goes first: on failure the row stays
	// unless "force" is checked, which deletes anyway.
	if err := s.Billing.DeleteCustomerForce(r.Context(), id, r.FormValue("force") == "1"); err != nil {
		if _, gerr := s.Queries.GetCustomer(r.Context(), id); gerr == nil {
			slog.Error("delete customer: router", "customer", c.Username, "err", err)
			s.Sessions.Put(r.Context(), "error", s.T("Not deleted, the router could not remove the customer"))
			http.Redirect(w, r, "/admin/customers/"+strconv.FormatInt(id, 10)+"?router_failed=1", http.StatusSeeOther)
			return
		}
		slog.Error("delete customer: router", "customer", c.Username, "err", err)
		s.logActivity(r, "customer.delete_router_failed", fmt.Sprintf("%s: %v", c.Username, err))
		s.Sessions.Put(r.Context(), "error", s.T("Deleted, but the router could not be updated"))
	}
	s.done(w, r, "/admin/customers", "Data Deleted Successfully", "customer.delete", c.Username)
}

// titleWords is PHP ucwords: the first letter of every word upper-case.
func titleWords(s string) string {
	b := []byte(s)
	for i := range b {
		if (i == 0 || b[i-1] == ' ') && b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}
