package adminx

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"
)

// MethodZero is the recharge method that charges nothing.
const MethodZero = "Zero"

// StrictAccess reports whether role_access_mode is strict (anything but "phpnuxbill").
func (s *Base) StrictAccess(ctx context.Context) bool {
	st, err := s.LoadSettings(ctx)
	return err != nil || st["role_access_mode"] != "phpnuxbill"
}

// CtxIsManager: the admin on ctx (set by requireAdmin) is SuperAdmin or Admin.
func CtxIsManager(ctx context.Context) bool {
	a, _ := ctx.Value(core.AdminKey{}).(*db.Admin)
	return a != nil && httpx.OneOf(a.Role, "SuperAdmin", "Admin")
}

func (s *Base) PlanOptions(r *http.Request, enabledOnly bool) ([]Option, map[int64]db.Plan, error) {
	plans, err := s.Queries.ListPlans(r.Context(), db.ListPlansParams{Limit: 1000})
	if err != nil {
		return nil, nil, err
	}
	var opts []Option
	byID := map[int64]db.Plan{}
	isoPlan, _ := s.IsolirIDs(r.Context())
	for _, p := range plans {
		byID[p.ID] = p // rows and names still resolve; only the options leave the isolir plan out
		if p.ID != isoPlan && (!enabledOnly || p.Enabled == 1) {
			opts = append(opts, Option{fmt.Sprint(p.ID), fmt.Sprintf("%s (%s)", p.Name, view.Money(p.Price))})
		}
	}
	return opts, byID, nil
}

// RechargeMethods lists what the admin recharge form offers: the payment_usings setting (PHP
// plan.php:80, default Cash), then Balance and Recharge Zero.
func (s *Base) RechargeMethods(ctx context.Context) []Option {
	st, _ := s.LoadSettings(ctx)
	var out []Option
	seen := map[string]bool{"balance": true, "zero": true}
	for _, u := range strings.Split(st["payment_usings"], ",") {
		if u = strings.TrimSpace(u); u != "" && !seen[strings.ToLower(u)] {
			seen[strings.ToLower(u)] = true
			out = append(out, Option{u, u})
		}
	}
	if len(out) == 0 {
		out = append(out, Option{"Cash", "Cash"})
	}
	if st["enable_balance"] != "no" { // like billing: balance is on unless explicitly off
		out = append(out, Option{"Balance", "Balance"})
	}
	if s.StrictAccess(ctx) && !CtxIsManager(ctx) { // strict: free recharge is for managers
		return out
	}
	return append(out, Option{MethodZero, "Recharge Zero"})
}

// SummaryLabels are the summary panel's strings in the page language; the script only fills values in.
// %s and %d placeholders are filled in order by the script.
func (s *Base) SummaryLabels() string {
	return JSONStr(map[string]string{
		"empty":     s.T("Select a customer to see the summary"),
		"error":     s.T("Could not load the summary"),
		"voucher":   s.T("Voucher account"),
		"voucherNo": s.T("Voucher code"),
		"balance":   s.T("Balance"),
		"active":    s.T("Active subscriptions"),
		"none":      s.T("No active subscription"),
		"txns":      s.T("Last 5 transactions"),
		"noTxns":    s.T("No transactions yet"),
		"date":      s.T("Date"),
		"invoice":   s.T("Invoice"),
		"plan":      s.T("Service Plan"),
		"price":     s.T("Price"),
		"view":      s.T("View customer"),
		"blocked":   s.T("This account is not active. Recharge will be refused until it is turned on."),
		"still":     s.T("Still active: %s until %s (%d days left)."),
		"extend":    s.T("Recharging this same plan extends it from that date."),
		"replace":   s.T("Recharging a different plan replaces it."),
	})
}

// AnyOpts is a select list: the "any" entry with label, then each value.
func AnyOpts(label string, o ...string) []Option {
	out := []Option{{"", label}}
	for _, x := range o {
		out = append(out, Option{x, x})
	}
	return out
}

// CustGet loads the customer of the {id} path value, or answers 404 or 500.
func (s *Base) CustGet(w http.ResponseWriter, r *http.Request) (db.Customer, bool) {
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

// SyncPlan tells the plan's router about a change. The DB change stays if the router fails;
// the admin gets a warning and the failure is logged.
func (s *Base) SyncPlan(r *http.Request, op string, p db.Plan, oldName string) {
	if s.Billing == nil {
		return
	}
	if err := s.Billing.SyncPlan(r.Context(), op, p, oldName); err != nil {
		slog.Error("sync plan", "op", op, "plan", p.Name, "err", err)
		s.LogActivity(r, "plan.sync_failed", fmt.Sprintf("%s %s: %v", op, p.Name, err))
		s.Sessions.Put(r.Context(), "error", s.T("Saved, but the router could not be updated")+": "+err.Error())
	}
}

// RouterNames lists up to 1000 routers; pools are meaningless without one.
func (s *Base) RouterNames(r *http.Request) ([]db.Router, error) {
	return s.Queries.ListRouters(r.Context(), db.ListRoutersParams{Limit: 1000})
}

// AdminFrom is the signed-in admin of the request.
func AdminFrom(r *http.Request) *db.Admin { return core.AdminFrom(r) }
