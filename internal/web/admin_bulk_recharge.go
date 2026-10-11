package web

// Bulk recharge from the customer list: "Recharge same plan" for the selected customers, paid in
// Cash. Each customer renews the plan they are on now, through chargePlan, so the rules are the
// single recharge's: extend_expiry, taxes, router sync and notifications.
//
// The money path is guarded by a one-time token: the confirm page stores the customers it found and
// the token in the session; the run consumes that token before it charges anything, so a second
// submit or a reload recharges nothing. The result is kept in the session for the result page.

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

const (
	bulkRechargeMax    = 200
	bulkPendingKey     = "bulk_recharge_pending" // the confirmed customers and token, consumed by the run
	bulkResultKey      = "bulk_recharge_result"  // the last run's summary, shown by the result page
	bulkResultPath     = "/admin/customers/recharge/result"
	bulkAlreadyHeading = "Already processed"
)

// bulkRechargeRow is one selected customer: the plan renewed, its price, and why it failed (Err is a
// message key; Note says the money was taken but the router missed the change).
type bulkRechargeRow struct {
	Username, Plan, Price string
	Err, Note             string
}

// bulkRechargePage is the confirm page (Token set), the result page (Done) or the "already processed" page.
type bulkRechargePage struct {
	Heading, Summary string // Summary: the count line on the confirm page, the result line after the run
	Done             bool
	Token            string // hidden field of the run form; empty when nothing can be recharged
	Rows             []bulkRechargeRow
}

// bulkRechargePending is what the confirm page leaves in the session for the run.
type bulkRechargePending struct {
	Token string  `json:"token"`
	Ids   []int64 `json:"ids"`
	Total int64   `json:"total"`
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

// newBulkToken is a random one-time token for the run form.
func newBulkToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// bulkRechargeConfirm: POST /admin/customers/recharge with the selected ids. It writes nothing; it
// shows the customers, their plans and prices, and the total to confirm. The customers that can be
// renewed and the total are kept in the session under a one-time token for the run.
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
	var eligible []int64
	var total int64
	for _, id := range ids {
		c, plan, price, reason := s.bulkRechargeCheck(r, id)
		row := bulkRechargeRow{Username: c.Username, Plan: plan.Name, Price: money(price), Err: reason}
		if reason != "" {
			row.Plan, row.Price = "", ""
		} else {
			eligible = append(eligible, id)
			total += price
		}
		p.Rows = append(p.Rows, row)
	}
	p.Summary = fmt.Sprintf(s.catalog.T(s.language(), "Recharge %d customers, total %s"), len(eligible), money(total))
	if len(eligible) > 0 {
		tok, err := newBulkToken()
		if err != nil {
			s.fail(w, "bulk recharge token", err)
			return
		}
		b, _ := json.Marshal(bulkRechargePending{Token: tok, Ids: eligible, Total: total})
		s.sessions.Put(r.Context(), bulkPendingKey, string(b))
		p.Token = tok
	}
	s.render(w, r, 200, "bulk_recharge", Page{Title: "Recharge same plan", Data: p})
}

// bulkAlreadyDone answers a run without a matching pending token: nothing is charged.
func (s *Server) bulkAlreadyDone(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusConflict, "bulk_recharge", Page{Title: "Recharge result", Data: bulkRechargePage{
		Heading: bulkAlreadyHeading, Done: true,
		Summary: s.catalog.T(s.language(), "This recharge was already processed or the form expired. Nothing was charged. Start again from the customer list."),
	}})
}

// bulkRechargeRun: POST /admin/customers/recharge/run with the one-time token. The token is consumed
// before anything is charged, so a second submit or a reload finds nothing. Only the customers stored
// with the token are processed; posted ids are ignored. The result goes to the session and the
// browser is redirected to the result page, which is safe to reload.
func (s *Server) bulkRechargeRun(w http.ResponseWriter, r *http.Request) {
	if s.Billing == nil {
		s.fail(w, "bulk recharge", errors.New("billing service not configured"))
		return
	}
	ctx := r.Context()
	raw := s.sessions.PopString(ctx, bulkPendingKey) // consumed here, whatever happens next
	var pend bulkRechargePending
	if raw == "" || json.Unmarshal([]byte(raw), &pend) != nil || pend.Token == "" ||
		subtle.ConstantTimeCompare([]byte(pend.Token), []byte(r.PostFormValue("token"))) != 1 {
		s.bulkAlreadyDone(w, r)
		return
	}
	// every recharge may call a router: the server's write timeout is too short for 200 of them
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Minute))
	p := bulkRechargePage{Heading: "Recharge result", Done: true}
	okN, failN := 0, 0
	for _, id := range pend.Ids {
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
	if b, err := json.Marshal(p); err == nil {
		s.sessions.Put(ctx, bulkResultKey, string(b))
	}
	s.logActivity(r, "customer.recharge.bulk", fmt.Sprintf("Cash, %d ok, %d failed (%s)", okN, failN, adminFrom(r).Username))
	http.Redirect(w, r, bulkResultPath, http.StatusSeeOther)
}

// bulkRechargeResult: GET /admin/customers/recharge/result shows the last run's summary. It only
// reads the session, so a reload shows the same page and charges nothing.
func (s *Server) bulkRechargeResult(w http.ResponseWriter, r *http.Request) {
	var p bulkRechargePage
	if raw := s.sessions.GetString(r.Context(), bulkResultKey); raw == "" || json.Unmarshal([]byte(raw), &p) != nil {
		s.bulkFail(w, r, "No recharge result to show yet.")
		return
	}
	s.render(w, r, 200, "bulk_recharge", Page{Title: "Recharge result", Data: p})
}
