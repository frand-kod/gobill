package web

// Customer list, detail, create, edit, save and delete.

import (
	"database/sql"
	"encoding/csv"
	"log/slog"
	"net/http"
	"net/mail"

	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"golang.org/x/crypto/bcrypt"
	"strconv"
	"strings"
	"time"
)

var (
	custStatuses = []string{"Active", "Banned", "Disabled", "Inactive", "Limited", "Suspended"}
	custNames    = []string{"username", "fullname", "address", "phone", "email", "service_type", "pppoe_username",
		"pppoe_ip", "billing_day", "auto_renewal", "status", "coordinates", "send_welcome_message", "notify_sms", "notify_wa", "notify_email"}
)

func custFields(v, e map[string]string, editing bool) []field {
	pw := text("password", "Password", v, e).as("password")
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
		text("phone", "Phone Number", v, e),
		text("email", "Email", v, e).as("email"),
		text("address", "Address", v, e),
		text("coordinates", "Coordinates", v, e).as("coords").hint("lat,lng, e.g. -6.2,106.8. Optional; shown on the customer map."),
	}, "Contact", "")
	svc := section([]field{
		text("service_type", "Service Type", v, e).opts("Hotspot", "PPPoE", "Others"),
		text("pppoe_username", "PPPoE Username", v, e),
		text("pppoe_ip", "PPPoE IP", v, e),
		sec,
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

var custSorts = []string{"username", "fullname", "balance", "status"}

// custQuery reads the list filters and sort; unknown values fall back to "any" / newest first.
func custQuery(r *http.Request) (db.FilterCustomersParams, string, string) {
	g := r.URL.Query().Get
	p := db.FilterCustomersParams{Q: strings.TrimSpace(g("q")), ServiceType: g("service_type"), Status: g("status")}
	if !oneOf(p.ServiceType, "Hotspot", "PPPoE", "Others") {
		p.ServiceType = ""
	}
	if !oneOf(p.Status, custStatuses...) {
		p.Status = ""
	}
	sort, dir := g("sort"), "asc"
	if !oneOf(sort, custSorts...) {
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

func (s *Server) custList(w http.ResponseWriter, r *http.Request) {
	p, sort, dir := custQuery(r)
	_, page, limit, off := paging(r)
	p.PageLimit, p.PageOffset = limit, off
	rows, err := s.queries.FilterCustomers(r.Context(), p)
	if err != nil {
		s.fail(w, "list customers", err)
		return
	}
	role := adminFrom(r).Role
	lp := listPage{Heading: "Customer", Base: "/admin/customers", Q: p.Q, Searchable: true, ViewLink: true,
		CanCreate: oneOf(role, "SuperAdmin", "Admin", "Agent", "Sales"), CanEdit: oneOf(role, "SuperAdmin", "Admin"),
		Cols:     []string{"Username", "Full Name", "Balance", "Package", "Service Type", "PPPoE Username", "Status"},
		SortKeys: []string{"username", "fullname", "balance", "", "", "", "status"}, Sort: sort, Dir: dir,
		Filters: []filter{{"service_type", p.ServiceType, anyOpts("Service Type", "Hotspot", "PPPoE", "Others")},
			{"status", p.Status, anyOpts("Status", custStatuses...)}}}
	if oneOf(role, "SuperAdmin", "Admin", "Agent", "Sales") {
		lp.Bulk = []bulkAction{{"message", "Send message to selected", false, false}}
	}
	for _, c := range rows {
		lp.Rows = append(lp.Rows, listRow{c.ID, []string{c.Username, c.Fullname, money(c.Balance), c.Packages, c.ServiceType, c.PppoeUsername, c.Status}})
	}
	lp.Links = []option{{"/admin/customers/export?" + lp.query().Encode(), "Export CSV"}}
	lp.finish(page)
	s.renderList(w, r, lp)
}

// custExport streams the current filter (all pages) as CSV, like the old customers csv.
func (s *Server) custExport(w http.ResponseWriter, r *http.Request) {
	p, _, _ := custQuery(r)
	p.PageLimit = -1
	rows, err := s.queries.FilterCustomers(r.Context(), p)
	if err != nil {
		s.fail(w, "export customers", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="customers-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"Username", "Full Name", "Phone Number", "Email", "Balance", "Package", "Service Type", "PPPoE Username", "Status", "Created"})
	for _, c := range rows {
		rec := []string{c.Username, c.Fullname, c.Phone, c.Email, fmt.Sprint(c.Balance), c.Packages, c.ServiceType, c.PppoeUsername, c.Status, s.ts(c.CreatedAt)}
		csvSafe(rec)
		cw.Write(rec)
	}
	cw.Flush()
}

func (s *Server) custNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"service_type": "Others", "status": "Active", "auto_renewal": "1"}
	fs, err := s.customerForm(r.Context(), 0, v, nil, false)
	if err != nil {
		s.fail(w, "custom fields", err)
		return
	}
	s.renderForm(w, r, 200, formPage{"Add New Contact", "/admin/customers", "/admin/customers", fs})
}

func (s *Server) custGet(w http.ResponseWriter, r *http.Request) (db.Customer, bool) {
	c, err := s.queries.GetCustomer(r.Context(), pathID(r))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return c, false
	} else if err != nil {
		s.fail(w, "get customer", err)
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
	fs, err := s.customerForm(r.Context(), c.ID, v, nil, true)
	if err != nil {
		s.fail(w, "custom fields", err)
		return
	}
	s.renderForm(w, r, 200, formPage{"Edit Contact: " + c.Username, fmt.Sprint("/admin/customers/", c.ID), "/admin/customers", fs})
}

type subRow struct{ Plan, Router, Expires, Status string }

type custDetail struct {
	Manage    bool // SuperAdmin/Admin: deactivate, sync, login as customer
	C         db.Customer
	Created   string
	SecretSet bool
	CanEdit   bool
	CanSell   bool // may recharge
	Plans     []option
	Methods   []option // payment methods offered on the recharge form
	Subs      []subRow
	Usage     *radiusUsage
	Trx       []db.Transaction
	Custom    []field // custom fields with this customer's values
	Online    string  // "" unless check_customer_online is yes; else Online, Offline or Error
}

func (s *Server) custView(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	role := adminFrom(r).Role
	d := custDetail{Manage: oneOf(role, "SuperAdmin", "Admin"), C: c, Created: s.ts(c.CreatedAt), SecretSet: len(c.SecretEnc) > 0,
		CanEdit: oneOf(role, "SuperAdmin", "Admin"), CanSell: oneOf(role, "SuperAdmin", "Admin", "Agent", "Sales")}
	opts, plans, err := s.planOptions(r, !d.Manage) // PHP: SuperAdmin/Admin may recharge a disabled plan
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	d.Plans = opts
	d.Methods = s.rechargeMethods(ctx)
	subs, err := s.queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
	if err != nil {
		s.fail(w, "list subscriptions", err)
		return
	}
	for _, x := range subs {
		d.Subs = append(d.Subs, subRow{plans[x.PlanID].Name, x.Type, s.ts(x.ExpiresAt), x.Status})
	}
	if d.Trx, err = s.queries.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 10}); err != nil {
		s.fail(w, "list transactions", err)
		return
	}
	d.Usage = s.radiusUsage(ctx, c)
	if d.Custom, err = s.cfFormFields(ctx, c.ID, map[string]string{}, nil); err != nil {
		s.fail(w, "custom fields", err)
		return
	}
	d.Online = s.customerOnline(ctx, c, subs)
	s.render(w, r, 200, "customer", Page{
		Title: c.Username,
		Flash: s.sessions.PopString(ctx, "flash"),
		Error: s.sessions.PopString(ctx, "error"),
		Data:  d,
	})
}

// customerOnline asks the device of the first active plan (old check_customer_online). Device
// errors show as Error, as the old red dot did.
func (s *Server) customerOnline(ctx context.Context, c db.Customer, subs []db.Subscription) string {
	st, err := s.loadSettings(ctx)
	if err != nil || s.Billing == nil || st["check_customer_online"] != "yes" {
		return ""
	}
	for _, x := range subs {
		if x.Status != "active" {
			continue
		}
		p, err := s.queries.GetPlan(ctx, x.PlanID)
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

// custSave serves both create (no {id} in the path) and update.
// setPassword stores a new portal password and, as in the old app (one password for portal and
// router), the same value as the router secret.
func (s *Server) setPassword(ctx context.Context, id int64, pass string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcryptCost)
	if err != nil {
		return err
	}
	enc, err := secret.Seal(s.SecretKey, []byte(pass))
	if err != nil {
		return err
	}
	if err = s.queries.SetCustomerPassword(ctx, db.SetCustomerPasswordParams{PasswordHash: string(hash), ID: id}); err != nil {
		return err
	}
	return s.queries.SetCustomerSecret(ctx, db.SetCustomerSecretParams{SecretEnc: enc, ID: id})
}

func (s *Server) custSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, custNames...)
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
	if v["username"] == "" {
		e["username"] = "This field is required"
	}
	// RADIUS logs in by username or pppoe_username, so neither may collide with another customer.
	for _, f := range []string{"username", "pppoe_username"} {
		if _, ok := e[f]; ok || v[f] == "" || id != 0 && v[f] == cur.Username && f == "username" || id != 0 && v[f] == cur.PppoeUsername && f == "pppoe_username" {
			continue
		}
		if _, err := s.queries.FindLoginNameOwner(r.Context(), db.FindLoginNameOwnerParams{Self: id, Name: v[f]}); err == nil {
			e[f] = "Username already used by another customer"
		} else if !errors.Is(err, sql.ErrNoRows) {
			s.fail(w, "check username", err)
			return
		}
	}
	if v["fullname"] == "" {
		e["fullname"] = "This field is required"
	}
	if v["email"] != "" {
		if _, err := mail.ParseAddress(v["email"]); err != nil {
			e["email"] = "Invalid email address"
		}
	}
	if !oneOf(v["service_type"], "Hotspot", "PPPoE", "Others") {
		e["service_type"] = "Invalid value"
	}
	if !oneOf(v["status"], custStatuses...) {
		e["status"] = "Invalid value"
	}
	coords, ok := checkCoords(v["coordinates"])
	if !ok {
		e["coordinates"] = "Enter coordinates as lat,lng, e.g. -6.2,106.8"
	}
	bday := sql.NullInt64{}
	if v["billing_day"] != "" {
		n, err := strconv.ParseInt(v["billing_day"], 10, 64)
		if err != nil || n < 1 || n > 31 {
			e["billing_day"] = "Enter a day between 1 and 31"
		}
		bday = sql.NullInt64{Int64: n, Valid: true}
	}
	cfv, err := s.cfCheck(r.Context(), r, v, e)
	if err != nil {
		s.fail(w, "check custom fields", err)
		return
	}
	if len(e) == 0 {
		enc := cur.SecretEnc
		if rs := cmp.Or(sec, pass); rs != "" { // one password like the old app; explicit secret wins
			var err error
			if enc, err = secret.Seal(s.SecretKey, []byte(rs)); err != nil {
				s.fail(w, "seal customer secret", err)
				return
			}
		}
		renew := int64(0)
		if v["auto_renewal"] == "1" {
			renew = 1
		}
		var err error
		ctx := r.Context()
		cid := id
		if id == 0 {
			var hash []byte
			if hash, err = bcrypt.GenerateFromPassword([]byte(pass), bcryptCost); err != nil {
				s.fail(w, "hash password", err)
				return
			}
			var c db.Customer
			c, err = s.queries.CreateCustomer(ctx, db.CreateCustomerParams{Username: v["username"], PasswordHash: string(hash),
				Fullname: v["fullname"], Address: v["address"], Phone: v["phone"], Email: v["email"], ServiceType: v["service_type"],
				PppoeUsername: v["pppoe_username"], PppoeIp: v["pppoe_ip"], SecretEnc: enc, AutoRenewal: renew,
				Status: v["status"], CreatedBy: sql.NullInt64{Int64: adminFrom(r).ID, Valid: true}, BillingDay: bday, Coordinates: coords})
			cid = c.ID
			if err == nil && v["send_welcome_message"] == "1" {
				s.welcome(ctx, c, welcomeChannels(v))
			}
		} else {
			if v["username"] != cur.Username { // first, so a clash changes nothing
				err = s.queries.RenameCustomer(ctx, db.RenameCustomerParams{Username: v["username"], ID: id})
			}
			if err == nil {
				err = s.queries.UpdateCustomer(ctx, db.UpdateCustomerParams{Fullname: v["fullname"], Address: v["address"],
					Phone: v["phone"], Email: v["email"], ServiceType: v["service_type"], PppoeUsername: v["pppoe_username"],
					PppoeIp: v["pppoe_ip"], SecretEnc: enc, AutoRenewal: renew, Status: v["status"], BillingDay: bday, Coordinates: coords, ID: id})
			}
			if err == nil && pass != "" {
				var hash []byte
				if hash, err = bcrypt.GenerateFromPassword([]byte(pass), bcryptCost); err == nil {
					err = s.queries.SetCustomerPassword(ctx, db.SetCustomerPasswordParams{PasswordHash: string(hash), ID: id})
				}
			}
		}
		if err == nil {
			err = s.cfStore(ctx, cid, cfv)
		}
		switch {
		case isUnique(err):
			e["username"] = "Username already exists"
		case err != nil:
			s.fail(w, "save customer", err)
			return
		default:
			act, msg := "customer.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "customer.update", "Data Updated Successfully"
				// A new password, username, pppoe username/ip goes to the router, as the old app did.
				if (pass != "" || sec != "" || v["username"] != cur.Username || v["pppoe_username"] != cur.PppoeUsername || v["pppoe_ip"] != cur.PppoeIp) && s.Billing != nil {
					if _, serr := s.Billing.SyncAfterEdit(r.Context(), cur, adminFrom(r).ID); serr != nil {
						slog.Error("sync customer", "customer", v["username"], "err", serr)
						s.logActivity(r, "customer.sync_failed", fmt.Sprintf("%s: %v", v["username"], serr))
						s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Saved, but the router could not be updated")+": "+serr.Error())
					}
				}
			}
			next := "/admin/customers"
			if id == 0 { // a new customer has no plan yet: go to the page where one is chosen
				next, msg = fmt.Sprint("/admin/customers/", cid), "Customer created. Choose a plan below to switch the internet on."
			}
			s.done(w, r, next, msg, act, v["username"])
			return
		}
	}
	action, head := "/admin/customers", "Add New Contact"
	if id != 0 {
		action, head = fmt.Sprint("/admin/customers/", id), "Edit Contact: "+cur.Username
	}
	fs, err := s.customerForm(r.Context(), id, v, e, id != 0)
	if err != nil {
		s.fail(w, "custom fields", err)
		return
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/customers", fs})
}

func (s *Server) custDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	c, err := s.queries.GetCustomer(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if s.Billing == nil {
		s.fail(w, "delete customer", errors.New("billing service not configured"))
		return
	}
	// Transactions are kept (customer_id -> NULL); a device error does not undo the delete.
	if err := s.Billing.DeleteCustomer(r.Context(), id); err != nil {
		if _, gerr := s.queries.GetCustomer(r.Context(), id); gerr == nil {
			s.fail(w, "delete customer", err)
			return
		}
		slog.Error("delete customer: router", "customer", c.Username, "err", err)
		s.logActivity(r, "customer.delete_router_failed", fmt.Sprintf("%s: %v", c.Username, err))
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Deleted, but the router could not be updated")+": "+err.Error())
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
