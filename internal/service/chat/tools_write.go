package chat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/money"
)

// Write tools. None of them acts on the first message: they check, show a preview and wait for the
// code (see confirm.go). The same checks run again when the code comes.

// Biller is the part of billing.Service the write tools use.
type Biller interface {
	Preview(ctx context.Context, customerID, planID int64) (billing.RechargePreview, error)
	Recharge(ctx context.Context, customerID, planID int64, method string, adminID int64) error
}

// Same lists as the admin pages: the customer page's canRecharge, and Managers for RADIUS sessions.
var (
	rolesRecharge = []string{"SuperAdmin", "Admin", "Agent", "Sales"}
	rolesManage   = []string{"SuperAdmin", "Admin"}
)

func init() {
	tools = append(tools,
		Tool{Name: "perpanjang", Aliases: []string{"recharge"}, Kind: KindAdmin, Roles: rolesRecharge, Run: (*Service).perpanjang},
		Tool{Name: "putus", Aliases: []string{"kick", "disconnect"}, Kind: KindAdmin, Roles: rolesManage, Run: (*Service).putus},
	)
}

// logWrite is the activity log line of an executed write: the action and text of the HTML page, plus the source.
func (s *Service) logWrite(ctx context.Context, adminID int64, action, desc string) {
	_ = s.Q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "admin", ActorID: adminID, Action: action, Description: desc + " source=wa"})
}

// pickPlan is the plan named by the admin, or the customer's current or last plan when none is named.
// It applies the Recharge page rules: a disabled plan only for SuperAdmin and Admin, never the isolir plan.
// The second result is a reply text when the plan cannot be used.
func (s *Service) pickPlan(ctx context.Context, cu db.Customer, name, role string) (db.Plan, string) {
	var p db.Plan
	if name == "" {
		var id int64
		if subs, err := s.Q.ListActiveSubscriptionsWithPlan(ctx, cu.ID); err == nil && len(subs) > 0 {
			id = subs[0].PlanID
		} else if trx, err := s.Q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: cu.ID, Valid: true}, Limit: 1}); err == nil && len(trx) > 0 {
			id = trx[0].PlanID.Int64
		}
		var err error
		if p, err = s.Q.GetPlan(ctx, id); err != nil {
			return p, s.t("No plan found. Name the plan: perpanjang <username> <plan>")
		}
	} else {
		plans, err := s.Q.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
		if err != nil {
			return p, s.failed()
		}
		found := false
		for _, x := range plans {
			if strings.EqualFold(x.Name, name) {
				p, found = x, true
				break
			}
		}
		if !found {
			return p, s.t("Plan not found.")
		}
	}
	if p.Type == "Balance" || p.ID == isolir.StoredPlanID(ctx, s.Q) || p.Enabled != 1 && role != "SuperAdmin" && role != "Admin" {
		return p, s.t("This plan cannot be used here.")
	}
	return p, ""
}

func (s *Service) perpanjang(ctx context.Context, c Caller, args string) string {
	user, planName, _ := strings.Cut(args, " ")
	planName = strings.TrimSpace(planName)
	if user == "" {
		return s.t("Recharge: perpanjang <username> [plan name]")
	}
	cu, err := s.Q.GetCustomerByUsername(ctx, user)
	if err != nil {
		return s.t("No customer found.")
	}
	if cu.Status != "Active" {
		return s.t("The account is not active.")
	}
	if s.Billing == nil {
		return s.failed()
	}
	p, msg := s.pickPlan(ctx, cu, planName, c.Role)
	if msg != "" {
		return msg
	}
	pv, err := s.Billing.Preview(ctx, cu.ID, p.ID)
	if err != nil {
		return s.failed()
	}
	preview := s.t("Recharge %s (%s) with %s for %s, paid in cash.", cu.Username, cu.Fullname, p.Name, money.Message(pv.Price))
	if !pv.Expiry.IsZero() {
		preview += "\n" + s.t("New expiry: %s", pv.Expiry.In(s.loc()).Format("2006-01-02 15:04"))
	}
	price, planID, custID, name := pv.Price, p.ID, cu.ID, p.Name
	return s.ask(c, rolesRecharge, preview, func(ctx context.Context, adminID int64) string {
		a, err := s.Q.GetAdmin(ctx, adminID)
		cu, err2 := s.Q.GetCustomer(ctx, custID)
		if err != nil || err2 != nil || cu.Status != "Active" {
			return s.failed()
		}
		p, msg := s.pickPlan(ctx, cu, name, a.Role)
		if msg != "" || p.ID != planID {
			return s.t("The plan changed. Send the command again.")
		}
		now, err := s.Billing.Preview(ctx, cu.ID, p.ID)
		if err != nil {
			return s.failed()
		}
		if now.Price != price {
			return s.t("The price changed to %s. Nothing was charged. Send the command again.", money.Message(now.Price))
		}
		ctx, devFailed := billing.TrackDeviceFailure(ctx)
		// the same method text the Recharge page records for Cash: "<method> - <admin name>"
		if err := s.Billing.Recharge(ctx, cu.ID, p.ID, "Cash - "+a.Fullname, a.ID); err != nil {
			if errors.Is(err, billing.ErrInactive) {
				return s.t("The account is not active.")
			}
			return s.failed()
		}
		desc, out := cu.Username+" ["+p.Name+"]", s.t("Done. %s recharged with %s.", cu.Username, p.Name)
		if *devFailed { // money and transaction are saved; only the router missed the change
			desc += " router not updated"
			out += "\n" + s.t("The router was not updated. Sync the customer in the admin page.")
		}
		s.logWrite(ctx, a.ID, "customer.recharge", desc)
		if trx, err := s.Q.ListTransactionsByCustomer(ctx, db.ListTransactionsByCustomerParams{CustomerID: sql.NullInt64{Int64: cu.ID, Valid: true}, Limit: 1}); err == nil && len(trx) > 0 {
			out += "\n" + s.t("Invoice: %s", trx[0].Invoice)
			if trx[0].PeriodEnd > 0 {
				out += "\n" + s.t("New expiry: %s", time.Unix(trx[0].PeriodEnd, 0).In(s.loc()).Format("2006-01-02 15:04"))
			}
		}
		return out
	})
}

func (s *Service) putus(ctx context.Context, c Caller, args string) string {
	user, _, _ := strings.Cut(args, " ")
	if user == "" {
		return s.t("Disconnect: putus <username>")
	}
	cu, err := s.Q.GetCustomerByUsername(ctx, user)
	if err != nil {
		return s.t("No customer found.")
	}
	if s.Disconnect == nil {
		return s.failed()
	}
	ss, err := s.Q.ListOpenRadiusSessionsByUser(ctx, cu.Username)
	if err != nil {
		return s.failed()
	}
	if len(ss) == 0 {
		return s.t("%s is not online.", cu.Username)
	}
	custID := cu.ID
	return s.ask(c, rolesManage, s.t("Disconnect %s (%s), %d online session(s).", cu.Username, cu.Fullname, len(ss)), func(ctx context.Context, adminID int64) string {
		cu, err := s.Q.GetCustomer(ctx, custID)
		if err != nil {
			return s.failed()
		}
		if err := s.Disconnect(ctx, cu.Username); err != nil {
			return s.t("Disconnect failed.")
		}
		s.logWrite(ctx, adminID, "radius.disconnect", cu.Username)
		return s.t("Done. %s was disconnected.", cu.Username)
	})
}
