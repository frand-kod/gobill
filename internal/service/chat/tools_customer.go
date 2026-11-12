package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/money"
	"github.com/frand-kod/gobill/internal/service/diagnose"
)

// ErrNoGateway: Pay has no gateway or no usable channel, so bayar points to the portal.
var ErrNoGateway = errors.New("no payment gateway")

const (
	reportGap  = 30 * time.Minute // one forwarded report per customer in this window
	payWindow  = time.Hour
	payMaxNew  = 3 // new payment requests per customer per window
	defaultAge = 900
)

// ponytail: in memory and per process, lost on restart (a customer may then report or pay-request again once).
// Move to a table if several instances ever run.
var recent = struct {
	sync.Mutex
	report map[int64]time.Time
	pay    map[int64][]time.Time
}{report: map[int64]time.Time{}, pay: map[int64][]time.Time{}}

// reserveReport is true when no report was forwarded for this customer in the last reportGap.
func reserveReport(id int64, now time.Time) bool {
	recent.Lock()
	defer recent.Unlock()
	if t, ok := recent.report[id]; ok && now.Sub(t) < reportGap {
		return false
	}
	recent.report[id] = now
	return true
}

// reservePay is true when the customer made fewer than payMaxNew requests in the last hour.
func reservePay(id int64, now time.Time) bool {
	recent.Lock()
	defer recent.Unlock()
	var keep []time.Time
	for _, t := range recent.pay[id] {
		if now.Sub(t) < payWindow {
			keep = append(keep, t)
		}
	}
	if len(keep) >= payMaxNew {
		recent.pay[id] = keep
		return false
	}
	recent.pay[id] = append(keep, now)
	return true
}

// gangguan: find the most likely cause for this customer's own account with the diagnose checks.
// With no cause found, the report goes to the active SuperAdmin and Admin by WhatsApp.
func (s *Service) gangguan(ctx context.Context, c Caller, _ string) string {
	cu, ok := s.customer(ctx, c)
	if !ok {
		return s.failed()
	}
	subs, err := s.Q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: cu.ID, Limit: 20})
	if err != nil {
		return s.failed()
	}
	stale := s.StaleFor
	if stale == nil {
		stale = func(string) int64 { return defaultAge }
	}
	d := (&diagnose.Service{Q: s.Q, Now: s.Now, StaleFor: stale,
		T:  s.t,
		TS: func(u int64) string { return time.Unix(u, 0).In(s.loc()).Format("02-01-2006 15:04") },
		Dur: func(sec int64) string {
			return (time.Duration(sec) * time.Second).Round(time.Minute).String()
		},
		Bytes: bytesText}).Diagnose(ctx, cu, subs, "")
	bad := func(label string) bool {
		i := d.Item(label)
		return i != nil && i.Cls != "badge-ok" && i.Cls != "badge-muted"
	}
	iso, _ := s.Q.GetRunningIsolation(ctx, db.GetRunningIsolationParams{CustomerID: cu.ID, Now: s.now().Unix()})
	switch {
	case bad("Account"):
		return s.t("Your account is %s. Please contact your provider.", s.t(cu.Status))
	case iso.ID != 0:
		return s.t("Your service is isolated. Please pay to activate it again.") + s.payHint()
	case bad("Plan"):
		return s.t("Your package has ended or has not started. Please pay to renew it.") + s.payHint()
	case bad("Quota"):
		return s.t("Your quota is used up. Please buy a new package.") + s.payHint()
	case d.Item("Router") != nil && d.Item("Router").Cls == "badge-bad": // only a router seen offline; "not checked yet" is not an outage
		return s.t("The network at your area is down. Our team is working on it, please wait.")
	case bad("Connection") && (d.Item("Connection").Short == s.t("Offline") || d.Item("Connection").Short == s.t("Silent")):
		return s.t("Your device looks offline. Turn the modem off, wait 30 seconds, turn it on again and check the cables.")
	}
	return s.t("We found no problem on our side. Restart your modem and try again.") + s.forward(ctx, cu)
}

func (s *Service) payHint() string {
	if s.PortalURL == "" {
		return ""
	}
	return "\n" + s.PortalURL
}

// forward tells the admins once per window. The returned text is for the customer.
func (s *Service) forward(ctx context.Context, cu db.Customer) string {
	if s.Notify == nil {
		return ""
	}
	if !reserveReport(cu.ID, s.now()) {
		return "\n" + s.t("Your report was already forwarded. Please wait for our team.")
	}
	admins, err := s.Q.SearchAdmins(ctx, db.SearchAdminsParams{Q: sql.NullString{Valid: true}, Scope: "all", PageLimit: 1000})
	if err != nil {
		slog.Error("chat: report admins", "err", err)
		return ""
	}
	msg := fmt.Sprintf("Gangguan: %s (%s), %s. %s", cu.Fullname, cu.Username, cu.Phone, s.now().In(s.loc()).Format("15:04"))
	for _, a := range admins {
		if a.Status == "Active" && (a.Role == "SuperAdmin" || a.Role == "Admin") && a.Phone != "" {
			if err := s.Notify(ctx, a.Phone, msg); err != nil {
				slog.Warn("chat: report send failed", "admin", a.ID, "err", err)
			}
		}
	}
	return "\n" + s.t("Your report was forwarded to our team.")
}

// bayar: the payment request for the customer's current plan, from the same code as the portal checkout.
func (s *Service) bayarGateway(ctx context.Context, c Caller, _ string) string {
	cu, ok := s.customer(ctx, c)
	if !ok {
		return s.failed()
	}
	if s.Pay == nil {
		return s.bayar(ctx, c, "")
	}
	now := s.now()
	if pr, err := s.Q.GetPendingPaymentRequestByCustomer(ctx, db.GetPendingPaymentRequestByCustomerParams{
		CustomerID: sql.NullInt64{Int64: cu.ID, Valid: true}, ExpiresAt: now.Unix()}); err == nil {
		return s.payText(ctx, pr)
	}
	plan, ok := s.renewPlan(ctx, cu.ID)
	if !ok {
		return s.t("No package to renew. Send paket to see the packages, or choose one at the customer portal.") + s.payHint()
	}
	if cu.Status != "Active" || !billing.SellablePlan(plan) {
		return s.t("This package cannot be paid here. Please open the customer portal of your provider to pay.") + s.payHint()
	}
	if !reservePay(cu.ID, now) {
		return s.t("Too many payment requests. Please try again later.")
	}
	pr, err := s.Pay(ctx, cu, plan)
	switch {
	case errors.Is(err, ErrNoGateway):
		return s.bayar(ctx, c, "")
	case err != nil:
		slog.Error("chat: pay", "err", err)
		return s.failed()
	}
	return s.payText(ctx, pr)
}

// renewPlan is the plan of the first active subscription, else of the newest one. Only a prepaid plan for sale counts.
func (s *Service) renewPlan(ctx context.Context, customerID int64) (db.Plan, bool) {
	var id int64
	if act, err := s.Q.ListActiveSubscriptionsWithPlan(ctx, customerID); err == nil && len(act) > 0 {
		id = act[0].PlanID
	} else if all, err := s.Q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: customerID, Limit: 1}); err == nil && len(all) > 0 {
		id = all[0].PlanID
	}
	p, err := s.Q.GetPlan(ctx, id)
	if id == 0 || err != nil || p.Enabled != 1 || p.Type == "Balance" || p.Billing != "prepaid" {
		return db.Plan{}, false
	}
	return p, true
}

func (s *Service) payText(ctx context.Context, pr db.PaymentRequest) string {
	name := ""
	if p, err := s.Q.GetPlan(ctx, pr.PlanID); err == nil {
		name = p.Name
	}
	out := s.t("Pay: %s %s", name, money.Message(pr.Amount))
	if pr.PayUrl != "" {
		out += "\n" + pr.PayUrl
	}
	return out + "\n" + s.t("Valid until %s", time.Unix(pr.ExpiresAt, 0).In(s.loc()).Format("02-01-2006 15:04"))
}
