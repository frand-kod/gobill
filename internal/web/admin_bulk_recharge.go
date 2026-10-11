package web

// Bulk recharge from the customer list: "Recharge same plan" for the selected customers, paid in
// Cash. Each customer renews the plan they are on now, through chargePlan, so the rules are the
// single recharge's: extend_expiry, taxes, router sync and notifications.

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

const bulkRechargeMax = 200

// bulkRechargeRow is one selected customer: the plan renewed, its price, and why it failed (Err is a
// message key; Note says the money was taken but the router missed the change).
type bulkRechargeRow struct {
	Username, Plan, Price string
	Err, Note             string
}

type bulkRechargePage struct {
	Heading, Summary string // Summary: the count line on the confirm page, the result line after the run
	Done             bool
	Ids              []int64 // customers that can be renewed: hidden fields for the run
	Rows             []bulkRechargeRow
}

// bulkFail sends the user back to the customer list with an error flash.
func (s *Server) bulkFail(w http.ResponseWriter, r *http.Request, msg string) {
	s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), msg))
	http.Redirect(w, r, "/admin/customers", http.StatusSeeOther)
}

// bulkRechargeIDs reads the posted ids: at least one, at most bulkRechargeMax, no duplicates.
func bulkRechargeIDs(r *http.Request) ([]int64, string) {
	if err := r.ParseForm(); err != nil {
		return nil, "Invalid selection"
	}
	var ids []int64
	seen := map[int64]bool{}
	for _, v := range r.PostForm["ids"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return nil, "Invalid selection"
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	switch {
	case len(ids) == 0:
		return nil, "Select at least one customer first"
	case len(ids) > bulkRechargeMax:
		return nil, "Select at most 200 customers"
	}
	return ids, ""
}

// bulkRechargeCheck finds the plan a customer would renew (the active one that ends last, not an
// isolation) and its price. reason is a message key, "" when the customer can be renewed.
func (s *Server) bulkRechargeCheck(r *http.Request, id int64) (c db.Customer, plan db.Plan, price int64, reason string) {
	ctx := r.Context()
	c, err := s.queries.GetCustomer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return c, plan, 0, "Customer not found"
	} else if err != nil {
		slog.Error("bulk recharge: customer", "id", id, "err", err)
		return c, plan, 0, msgRechargeFail
	}
	if c.Status != "Active" {
		return c, plan, 0, "This customer is not active. Turn the customer on first."
	}
	subs, err := s.queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: id, Limit: 1000})
	if err != nil {
		slog.Error("bulk recharge: subscriptions", "id", id, "err", err)
		return c, plan, 0, msgRechargeFail
	}
	var cur *db.Subscription
	for i := range subs {
		if x := &subs[i]; x.Status == "active" && x.Method != "Isolir" && (cur == nil || x.ExpiresAt > cur.ExpiresAt) {
			cur = x
		}
	}
	if cur == nil {
		return c, plan, 0, "No plan to renew: recharge this customer one by one"
	}
	plan, err = s.queries.GetPlan(ctx, cur.PlanID)
	if err != nil {
		return c, plan, 0, msgPlanMissing
	}
	// same rule as the single recharge: Agent and Sales may not renew a disabled plan
	if plan.Enabled != 1 && !oneOf(adminFrom(r).Role, "SuperAdmin", "Admin") {
		return c, plan, 0, planErrMsg(true, plan.Enabled)
	}
	pv, err := s.Billing.Preview(ctx, id, plan.ID)
	if err != nil {
		return c, plan, 0, msgRechargeFail
	}
	return c, plan, pv.Price, ""
}

// bulkRechargeConfirm: POST /admin/customers/recharge with the selected ids. It writes nothing; it
// shows the customers, their plans and prices, and the total to confirm.
func (s *Server) bulkRechargeConfirm(w http.ResponseWriter, r *http.Request) {
	ids, msg := bulkRechargeIDs(r)
	if msg != "" {
		s.bulkFail(w, r, msg)
		return
	}
	if s.Billing == nil {
		s.fail(w, "bulk recharge", errors.New("billing service not configured"))
		return
	}
	p := bulkRechargePage{Heading: "Recharge same plan"}
	var total int64
	for _, id := range ids {
		c, plan, price, reason := s.bulkRechargeCheck(r, id)
		row := bulkRechargeRow{Username: c.Username, Plan: plan.Name, Price: money(price), Err: reason}
		if reason != "" {
			row.Plan, row.Price = "", ""
		} else {
			p.Ids = append(p.Ids, id)
			total += price
		}
		p.Rows = append(p.Rows, row)
	}
	p.Summary = fmt.Sprintf(s.catalog.T(s.language(), "Recharge %d customers, total %s"), len(p.Ids), money(total))
	s.render(w, r, 200, "bulk_recharge", Page{Title: "Recharge same plan", Data: p})
}

// bulkRechargeRun: POST /admin/customers/recharge/run with the confirmed ids. Renews each customer
// in turn (Cash) and shows the per-customer results.
func (s *Server) bulkRechargeRun(w http.ResponseWriter, r *http.Request) {
	ids, msg := bulkRechargeIDs(r)
	if msg != "" {
		s.bulkFail(w, r, msg)
		return
	}
	if s.Billing == nil {
		s.fail(w, "bulk recharge", errors.New("billing service not configured"))
		return
	}
	// every recharge may call a router: the server's write timeout is too short for 200 of them
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Minute))
	admin := adminFrom(r)
	p := bulkRechargePage{Heading: "Recharge result", Done: true}
	okN, failN := 0, 0
	for _, id := range ids {
		c, plan, _, reason := s.bulkRechargeCheck(r, id)
		row := bulkRechargeRow{Username: c.Username, Plan: plan.Name, Err: reason}
		if reason == "" {
			devFailed, err := s.chargePlan(r, c, plan, "Cash", "Cash")
			switch {
			case err != nil:
				row.Err = rechargeFailMsg(err, c, plan)
			case devFailed:
				row.Note = msgNotSynced
			}
		}
		if row.Err != "" {
			failN++
		} else {
			okN++
		}
		p.Rows = append(p.Rows, row)
	}
	p.Summary = fmt.Sprintf(s.catalog.T(s.language(), "%d recharged, %d failed"), okN, failN)
	s.logActivity(r, "customer.recharge.bulk", fmt.Sprintf("Cash, %d ok, %d failed (%s)", okN, failN, admin.Username))
	s.render(w, r, 200, "bulk_recharge", Page{Title: "Recharge result", Data: p})
}
