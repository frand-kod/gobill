package web

// Customer summary for the recharge page: status, balance, active subscriptions and the last
// transactions of the chosen customer, plus the double-charge guard data.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
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
	Usage     string `json:"usage"`     // data or time used of the limit, from the subscription counters
	QuotaOut  bool   `json:"quota_out"` // a limit is used up: recharging is what brings the customer back
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
		StatusLabel: s.t(c.Status), BlocksRecharge: c.Status != "Active",
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
		if row.Usage, row.QuotaOut, err = s.summaryUsage(ctx, c, sb.ID, sb.PlanID); err != nil {
			s.fail(w, "customer summary", err)
			return
		}
		if sb.PendingStart == 1 {
			row.ExpiresAt, row.DaysLeft = s.subExpiry(1, 0), 0 // no date until the first login
		}
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

// summaryUsage is the usage line of one active subscription: data and time used of the plan limit
// (planQuota, the numbers the RADIUS check enforces), or the data used since the plan started when it has
// no limit; a time-only limit also shows the data. "" for a non-RADIUS plan. full: a limit is used up.
func (s *Server) summaryUsage(ctx context.Context, c db.Customer, subID, planID int64) (line string, full bool, err error) {
	sub, err := s.queries.GetSubscription(ctx, subID)
	if err != nil {
		return "", false, err
	}
	plan, err := s.queries.GetPlan(ctx, planID)
	if err != nil {
		return "", false, err
	}
	if plan.Device != "Radius" {
		return "", false, nil // only RADIUS accounting fills the counters: a MikroTik plan would always read 0 B
	}
	q := s.planQuota(ctx, c, sub, plan)
	var parts []string
	if q.HasData {
		parts = append(parts, fmt.Sprintf(s.t("Data used: %s of %s"), humanBytes(q.Data), humanBytes(q.DataLim)))
		full = full || q.Data >= q.DataLim
	}
	if q.HasTime {
		parts = append(parts, fmt.Sprintf(s.t("Time used: %s of %s"), humanDur(q.Time), humanDur(q.TimeLim)))
		full = full || q.Time >= q.TimeLim
	}
	switch {
	case len(parts) == 0:
		parts = append(parts, fmt.Sprintf(s.t("Used since plan start: %s"), humanBytes(sub.UsedInOctets+sub.UsedOutOctets)))
	case !q.HasData: // a time limit only: the data is still worth seeing
		parts = append(parts, fmt.Sprintf(s.t("Data: %s"), humanBytes(sub.UsedInOctets+sub.UsedOutOctets)))
	}
	if full {
		parts = append([]string{s.t("Quota used up")}, parts...)
	}
	return strings.Join(parts, " · "), full, nil
}

// summaryLabels are the summary panel's strings in the page language; the script only fills values in.
// %s and %d placeholders are filled in order by the script.
func (s *Server) summaryLabels() string {
	return jsonStr(map[string]string{
		"empty":     s.t("Select a customer to see the summary"),
		"error":     s.t("Could not load the summary"),
		"voucher":   s.t("Voucher account"),
		"voucherNo": s.t("Voucher code"),
		"balance":   s.t("Balance"),
		"active":    s.t("Active subscriptions"),
		"none":      s.t("No active subscription"),
		"txns":      s.t("Last 5 transactions"),
		"noTxns":    s.t("No transactions yet"),
		"date":      s.t("Date"),
		"invoice":   s.t("Invoice"),
		"plan":      s.t("Service Plan"),
		"price":     s.t("Price"),
		"view":      s.t("View customer"),
		"blocked":   s.t("This account is not active. Recharge will be refused until it is turned on."),
		"still":     s.t("Still active: %s until %s (%d days left)."),
		"extend":    s.t("Recharging this same plan extends it from that date."),
		"replace":   s.t("Recharging a different plan replaces it."),
	})
}
