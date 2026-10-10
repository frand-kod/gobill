package web

// Customer summary for the recharge page: status, balance, active subscriptions and the last
// transactions of the chosen customer, plus the double-charge guard data.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// summarySub is an active subscription in the customer summary. Effect says what recharging the
// chosen plan does to it: "extend" (same plan, runs on from its expiry), "replace" (another plan on
// the same router and type takes its place), or "" (no conflict, or no plan chosen).
type summarySub struct {
	Plan      string `json:"plan"`
	Type      string `json:"type"`
	ExpiresAt string `json:"expires_at"`
	DaysLeft  int    `json:"days_left"`
	Effect    string `json:"effect"`
}

type summaryTrx struct {
	Date    string `json:"date"`
	Invoice string `json:"invoice"`
	Plan    string `json:"plan"`
	Price   string `json:"price"`
	Method  string `json:"method"`
}

// summaryVoucher marks a voucher account (one that redeemed a voucher); its personal name is left out.
type summaryVoucher struct {
	Code string `json:"code"`
	Plan string `json:"plan"`
}

// customerSummary is what the recharge page shows for the chosen customer. It carries no phone,
// email, address, coordinates or secrets.
type customerSummary struct {
	ID             int64           `json:"id"`
	Username       string          `json:"username"`
	Fullname       string          `json:"fullname"`
	Voucher        *summaryVoucher `json:"voucher"`
	Status         string          `json:"status"`
	StatusLabel    string          `json:"status_label"`
	BlocksRecharge bool            `json:"blocks_recharge"`
	Balance        string          `json:"balance"`
	Subscriptions  []summarySub    `json:"subscriptions"`
	Transactions   []summaryTrx    `json:"transactions"`
}

// custSummary: GET /admin/customers/{id}/summary?plan=<id>. Read-only. plan is optional and only
// used to say what a recharge with it would do to the active subscriptions (same rule as billing).
func (s *Server) custSummary(w http.ResponseWriter, r *http.Request) {
	c, ok := s.custGet(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	st, err := s.loadSettings(ctx)
	if err != nil {
		s.fail(w, "customer summary", err)
		return
	}
	loc := s.location()
	out := customerSummary{ID: c.ID, Username: c.Username, Fullname: c.Fullname, Status: c.Status,
		StatusLabel: s.catalog.T(s.language(), c.Status), BlocksRecharge: c.Status != "Active",
		Balance: money(c.Balance), Subscriptions: []summarySub{}, Transactions: []summaryTrx{}}

	v, err := s.queries.LastVoucherUsedBy(ctx, sql.NullInt64{Int64: c.ID, Valid: true})
	switch {
	case err == nil:
		out.Voucher, out.Fullname = &summaryVoucher{Code: v.Code, Plan: v.PlanName}, ""
	case !errors.Is(err, sql.ErrNoRows):
		s.fail(w, "customer summary", err)
		return
	}

	var plan db.Plan
	hasPlan := false
	if pid, ok := posInt(r.URL.Query().Get("plan")); ok {
		plan, err = s.queries.GetPlan(ctx, pid)
		switch {
		case err == nil:
			hasPlan = true
		case !errors.Is(err, sql.ErrNoRows):
			s.fail(w, "customer summary", err)
			return
		}
	}
	subs, err := s.queries.ListActiveSubscriptionsWithPlan(ctx, c.ID)
	if err != nil {
		s.fail(w, "customer summary", err)
		return
	}
	now := time.Now()
	for _, sb := range subs {
		exp := time.Unix(sb.ExpiresAt, 0).In(loc)
		row := summarySub{Plan: sb.PlanName, Type: sb.Type, ExpiresAt: exp.Format("2006-01-02 15:04"),
			DaysLeft: max(0, int(math.Ceil(exp.Sub(now).Hours()/24)))}
		// billing (activeSub + recharge): the active subscription on the same router and type is renewed
		if hasPlan && plan.Type != "Balance" && sb.Type == plan.Type && sb.RouterID == plan.RouterID {
			row.Effect = "replace"
			if sb.PlanID == plan.ID && st["extend_expiry"] != "no" {
				row.Effect = "extend"
			}
		}
		out.Subscriptions = append(out.Subscriptions, row)
	}

	trx, err := s.queries.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{
		CustomerID: sql.NullInt64{Int64: c.ID, Valid: true}, Limit: 5})
	if err != nil {
		s.fail(w, "customer summary", err)
		return
	}
	for _, t := range trx {
		out.Transactions = append(out.Transactions, summaryTrx{Date: time.Unix(t.CreatedAt, 0).In(loc).Format("2006-01-02 15:04"),
			Invoice: t.Invoice, Plan: t.PlanName, Price: money(t.Price), Method: t.Method})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

// summaryLabels are the summary panel's strings in the page language; the script only fills values in.
// %s and %d placeholders are filled in order by the script.
func (s *Server) summaryLabels() string {
	t := func(k string) string { return s.catalog.T(s.language(), k) }
	return jsonStr(map[string]string{
		"empty":     t("Select a customer to see the summary"),
		"error":     t("Could not load the summary"),
		"voucher":   t("Voucher account"),
		"voucherNo": t("Voucher code"),
		"balance":   t("Balance"),
		"active":    t("Active subscriptions"),
		"none":      t("No active subscription"),
		"txns":      t("Last 5 transactions"),
		"noTxns":    t("No transactions yet"),
		"date":      t("Date"),
		"invoice":   t("Invoice"),
		"plan":      t("Service Plan"),
		"price":     t("Price"),
		"view":      t("View customer"),
		"blocked":   t("This account is not active. Recharge will be refused until it is turned on."),
		"still":     t("Still active: %s until %s (%d days left)."),
		"extend":    t("Recharging this same plan extends it from that date."),
		"replace":   t("Recharging a different plan replaces it."),
	})
}
