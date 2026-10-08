package web

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"

	"golang.org/x/crypto/bcrypt"

	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
)

var (
	custStatuses = []string{"Active", "Banned", "Disabled", "Inactive", "Limited", "Suspended"}
	custNames    = []string{"username", "fullname", "address", "phone", "email", "service_type", "pppoe_username",
		"pppoe_ip", "billing_day", "auto_renewal", "status"}
)

func custFields(v, e map[string]string, editing bool) []field {
	pw := text("password", "Password", v, e).as("password")
	sec := text("secret", "Router Secret", v, e).as("password").hint("Hotspot/PPPoE password on the router. Leave empty to keep the current one.")
	ar := text("auto_renewal", "Auto Renewal", v, e).as("checkbox")
	ar.Checked = v["auto_renewal"] == "1"
	fs := []field{}
	if editing {
		pw.Hint = "Leave empty to keep the current password"
	} else {
		pw.Required = true
		fs = append(fs, text("username", "Username", v, e).req())
	}
	acct := section(append(fs, pw, text("fullname", "Full Name", v, e).req(), text("status", "Status", v, e).opts(custStatuses...)), "Account", "")
	contact := section([]field{
		text("phone", "Phone Number", v, e),
		text("email", "Email", v, e).as("email"),
		text("address", "Address", v, e),
	}, "Contact", "")
	svc := section([]field{
		text("service_type", "Service Type", v, e).opts("Hotspot", "PPPoE", "Others"),
		text("pppoe_username", "PPPoE Username", v, e),
		text("pppoe_ip", "PPPoE IP", v, e),
		sec,
		text("billing_day", "Billing Day", v, e).as("number").hint("Day of month, 1-31. Optional; overrides the plan."),
		ar,
	}, "Service & billing", "")
	return append(append(acct, contact...), svc...)
}

func (s *Server) custList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchCustomers(r.Context(), db.SearchCustomersParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list customers", err)
		return
	}
	role := adminFrom(r).Role
	lp := listPage{Heading: "Customer", Base: "/admin/customers", Q: q, Searchable: true, ViewLink: true,
		CanCreate: oneOf(role, "SuperAdmin", "Admin", "Agent", "Sales"), CanEdit: oneOf(role, "SuperAdmin", "Admin"),
		Cols: []string{"Username", "Full Name", "Phone Number", "Service Type", "Status"}}
	for _, c := range rows {
		lp.Rows = append(lp.Rows, listRow{c.ID, []string{c.Username, c.Fullname, c.Phone, c.ServiceType, c.Status}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) custNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"service_type": "Others", "status": "Active", "auto_renewal": "1"}
	s.renderForm(w, r, 200, formPage{"Add New Contact", "/admin/customers", "/admin/customers", custFields(v, nil, false)})
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
	v := map[string]string{"fullname": c.Fullname, "address": c.Address, "phone": c.Phone, "email": c.Email,
		"service_type": c.ServiceType, "pppoe_username": c.PppoeUsername, "pppoe_ip": c.PppoeIp,
		"auto_renewal": fmt.Sprint(c.AutoRenewal), "status": c.Status}
	if c.BillingDay.Valid {
		v["billing_day"] = fmt.Sprint(c.BillingDay.Int64)
	}
	s.renderForm(w, r, 200, formPage{"Edit Contact: " + c.Username, fmt.Sprint("/admin/customers/", c.ID), "/admin/customers", custFields(v, nil, true)})
}

type subRow struct{ Plan, Router, Expires, Status string }

type custDetail struct {
	C         db.Customer
	Created   string
	SecretSet bool
	CanEdit   bool
	CanSell   bool // may recharge
	Plans     []option
	Subs      []subRow
	Usage     *radiusUsage
	Trx       []db.Transaction
}

func (s *Server) custView(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	role := adminFrom(r).Role
	d := custDetail{C: c, Created: s.ts(c.CreatedAt), SecretSet: len(c.SecretEnc) > 0,
		CanEdit: oneOf(role, "SuperAdmin", "Admin"), CanSell: oneOf(role, "SuperAdmin", "Admin", "Agent", "Sales")}
	opts, plans, err := s.planOptions(r, true)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	d.Plans = opts
	subs, err := s.queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 50})
	if err != nil {
		s.fail(w, "list subscriptions", err)
		return
	}
	for _, x := range subs {
		d.Subs = append(d.Subs, subRow{plans[x.PlanID].Name, x.Type, s.ts(x.ExpiresAt), x.Status})
	}
	if d.Trx, err = s.queries.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: c.ID, Limit: 10}); err != nil {
		s.fail(w, "list transactions", err)
		return
	}
	d.Usage = s.radiusUsage(ctx, c)
	s.render(w, r, 200, "customer", Page{
		Title: c.Username,
		Flash: s.sessions.PopString(ctx, "flash"),
		Error: s.sessions.PopString(ctx, "error"),
		Data:  d,
	})
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
	if err != nil || plan.Enabled != 1 {
		fail("Invalid plan")
		return
	}
	admin := adminFrom(r).ID
	switch r.PostFormValue("method") {
	case "Cash":
		err = s.Billing.Recharge(r.Context(), c.ID, plan.ID, "Admin - Cash", admin)
	case "Balance":
		err = s.Billing.RechargeWithBalance(r.Context(), c.ID, plan.ID, admin)
	default:
		fail("Invalid payment method")
		return
	}
	if errors.Is(err, billing.ErrInsufficientBalance) {
		fail("Insufficient balance")
		return
	} else if err != nil {
		slog.Error("recharge", "customer", c.Username, "plan", plan.Name, "err", err)
		fail("Recharge failed")
		return
	}
	s.done(w, r, back, "Recharge Successful", "customer.recharge", c.Username+" ["+plan.Name+"]")
}

// custSave serves both create (no {id} in the path) and update.
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
		v["username"] = cur.Username
	} else {
		if v["username"] == "" {
			e["username"] = "This field is required"
		}
		if pass == "" {
			e["password"] = "This field is required"
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
	bday := sql.NullInt64{}
	if v["billing_day"] != "" {
		n, err := strconv.ParseInt(v["billing_day"], 10, 64)
		if err != nil || n < 1 || n > 31 {
			e["billing_day"] = "Enter a day between 1 and 31"
		}
		bday = sql.NullInt64{Int64: n, Valid: true}
	}
	if len(e) == 0 {
		enc := cur.SecretEnc
		if sec != "" {
			var err error
			if enc, err = secret.Seal(s.SecretKey, []byte(sec)); err != nil {
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
		if id == 0 {
			var hash []byte
			if hash, err = bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost); err != nil {
				s.fail(w, "hash password", err)
				return
			}
			_, err = s.queries.CreateCustomer(ctx, db.CreateCustomerParams{Username: v["username"], PasswordHash: string(hash),
				Fullname: v["fullname"], Address: v["address"], Phone: v["phone"], Email: v["email"], ServiceType: v["service_type"],
				PppoeUsername: v["pppoe_username"], PppoeIp: v["pppoe_ip"], SecretEnc: enc, AutoRenewal: renew,
				Status: v["status"], CreatedBy: sql.NullInt64{Int64: adminFrom(r).ID, Valid: true}, BillingDay: bday})
		} else {
			err = s.queries.UpdateCustomer(ctx, db.UpdateCustomerParams{Fullname: v["fullname"], Address: v["address"],
				Phone: v["phone"], Email: v["email"], ServiceType: v["service_type"], PppoeUsername: v["pppoe_username"],
				PppoeIp: v["pppoe_ip"], SecretEnc: enc, AutoRenewal: renew, Status: v["status"], BillingDay: bday, ID: id})
			if err == nil && pass != "" {
				var hash []byte
				if hash, err = bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost); err == nil {
					err = s.queries.SetCustomerPassword(ctx, db.SetCustomerPasswordParams{PasswordHash: string(hash), ID: id})
				}
			}
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
			}
			s.done(w, r, "/admin/customers", msg, act, v["username"])
			return
		}
	}
	action, head := "/admin/customers", "Add New Contact"
	if id != 0 {
		action, head = fmt.Sprint("/admin/customers/", id), "Edit Contact: "+cur.Username
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/customers", custFields(v, e, id != 0)})
}

func (s *Server) custDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if c, err := s.queries.GetCustomer(r.Context(), pathID(r)); err == nil {
		name = c.Username
	}
	s.remove(w, r, "/admin/customers", "customer", "Customer has transactions and cannot be deleted", name, func(id int64) error {
		return s.queries.DeleteCustomer(r.Context(), id)
	})
}
