package billing

// ISOLIR: an expired customer is moved into the isolir plan for isolir_days (0 = until paid).
// The isolation is an ordinary subscription on the isolir plan, so its start and end are the
// subscription's started_at and expires_at. A recharge or voucher ends it.

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/isolir"
)

// isolate starts the isolation of an expired subscription. It runs after the expiry is committed
// and no auto-renewal happened. It does nothing when isolir_enabled is not "yes", the plan has its
// own expired_plan_id (explicit wins), the plan is the isolir plan itself, or the customer still has
// another active subscription.
func (s *Service) isolate(ctx context.Context, plan db.Plan, c db.Customer) error {
	if setting(ctx, s.Q, "isolir_enabled") != "yes" || plan.ExpiredPlanID.Valid {
		return nil
	}
	planID, err := isolir.EnsurePlan(ctx, s.Q)
	if err != nil {
		return fmt.Errorf("isolir plan: %w", err)
	}
	if plan.ID == planID {
		return nil
	}
	iso, err := s.Q.GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	days := isolir.Days(setting(ctx, s.Q, "isolir_days"))
	now := s.now()
	return s.tx(ctx, func(q *db.Queries) error {
		subs, err := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 1000})
		if err != nil {
			return err
		}
		for _, sub := range subs {
			if sub.Status == "active" {
				return nil
			}
		}
		_, err = q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: c.ID, PlanID: iso.ID, RouterID: iso.RouterID,
			Type: iso.Type, StartedAt: now.Unix(), ExpiresAt: isolir.Until(now, days).Unix(), Method: "Isolir",
			AdminID: sql.NullInt64{}, PendingStart: 0})
		if err != nil {
			return err
		}
		return q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "system", ActorID: 0, Action: "isolir",
			Description: fmt.Sprintf("%s isolir %d hari (%s)", c.Username, days, plan.Name)})
	})
}

// endIsolation expires the customer's active isolir subscription so a recharge to keepPlanID can
// take over. It reports whether an isolation was ended. Runs inside the caller's transaction.
func endIsolation(ctx context.Context, q *db.Queries, now int64, customerID, keepPlanID int64) (bool, error) {
	iso := isolir.PlanID(setting(ctx, q, "isolir_plan_id"))
	if iso == 0 || iso == keepPlanID {
		return false, nil
	}
	subs, err := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: customerID, Limit: 1000})
	if err != nil {
		return false, err
	}
	ended := false
	for _, sub := range subs {
		if sub.Status == "active" && sub.PlanID == iso {
			n, err := q.DeactivateSubscription(ctx, db.DeactivateSubscriptionParams{Now: now, ID: sub.ID})
			if err != nil {
				return false, err
			}
			ended = ended || n > 0
		}
	}
	return ended, nil
}

// disconnectIsolated drops the customer's open RADIUS sessions after an isolation ended, so the next
// Access-Request gets the normal plan instead of the isolir address-list and rate limit.
func (s *Service) disconnectIsolated(ctx context.Context, c db.Customer) {
	dev := device.Radius{Q: s.Q, Key: s.Key}
	if err := dev.Disconnect(ctx, device.Customer{Username: c.Username}, ""); err != nil {
		slog.Error("isolir: disconnect after recharge", "customer", c.Username, "err", err)
	}
}
