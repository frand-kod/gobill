package billing

// ISOLIR: an expired customer is isolated for isolir_days (0 = until paid). The isolation is a row in
// isolations, not a subscription: it holds the customer's access state while the plan list and the
// subscriptions stay as they are. A purchase or a voucher ends it, and so does the end of its days.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/isolir"
)

// isolate starts the isolation of an expired subscription. It runs after the expiry is committed
// and no auto-renewal happened. It does nothing when isolir_enabled is not "yes", the plan has its
// own expired_plan_id (explicit wins), the plan is not on the Radius device (a router plan cannot be
// isolated), the customer is not Active, the customer already has a running isolation, or the customer
// still has another active subscription.
func (s *Service) isolate(ctx context.Context, plan db.Plan, c db.Customer) error {
	if setting(ctx, s.Q, "isolir_enabled") != "yes" || plan.ExpiredPlanID.Valid || plan.Device != "Radius" || c.Status != "Active" {
		return nil
	}
	if _, err := isolir.EnsurePlan(ctx, s.Q); err != nil {
		return fmt.Errorf("isolir plan: %w", err)
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
		if _, err := q.GetRunningIsolation(ctx, db.GetRunningIsolationParams{CustomerID: c.ID, Now: now.Unix()}); err == nil {
			return nil
		}
		if _, err := q.CreateIsolation(ctx, db.CreateIsolationParams{CustomerID: c.ID, StartedAt: now.Unix(), EndsAt: isolir.Until(now, days).Unix()}); err != nil {
			return err
		}
		return q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "system", ActorID: 0, Action: "isolir",
			Description: fmt.Sprintf("%s isolir %d hari (%s)", c.Username, days, plan.Name)})
	})
}

// isolirPlan reports whether planID is the isolir plan. The isolir plan is a system plan: it is never
// recharged or assigned by hand, and no subscription uses it.
func isolirPlan(ctx context.Context, q *db.Queries, planID int64) bool {
	v, err := q.GetSetting(ctx, "isolir_plan_id")
	return err == nil && planID != 0 && isolir.PlanID(v) == planID
}

// endIsolation ends the customer's running isolation because a plan was bought (reason "paid"). It
// reports whether one ended. Runs inside the caller's transaction.
func endIsolation(ctx context.Context, q *db.Queries, now int64, customerID int64, reason string) (bool, error) {
	n, err := q.EndCustomerIsolations(ctx, db.EndCustomerIsolationsParams{CustomerID: customerID, Now: now, Reason: reason})
	return n > 0, err
}

// closeIsolation ends one running isolation and drops the customer's open RADIUS sessions, so the next
// Access-Request gets the normal answer. A row that already ended is left alone.
func (s *Service) closeIsolation(ctx context.Context, iso db.Isolation, reason string) error {
	n, err := s.Q.EndIsolation(ctx, db.EndIsolationParams{ID: iso.ID, Now: s.now().Unix(), Reason: reason})
	if err != nil || n == 0 {
		return err
	}
	c, err := s.Q.GetCustomer(ctx, iso.CustomerID)
	if err != nil {
		return err
	}
	s.disconnectIsolated(ctx, c)
	return nil
}

// endDueIsolations ends the isolations whose days ran out. The expiry job calls it every run.
func (s *Service) endDueIsolations(ctx context.Context) error {
	dues, err := s.Q.ListDueIsolations(ctx, s.now().Unix())
	if err != nil {
		return err
	}
	for _, iso := range dues {
		if err := s.closeIsolation(ctx, iso, "expired"); err != nil {
			slog.Error("isolir: end expired isolation", "isolation", iso.ID, "err", err)
		}
	}
	return nil
}

// disconnectIsolated drops the customer's open RADIUS sessions after an isolation ended, so the next
// Access-Request gets the normal plan instead of the isolir address-list and rate limit.
func (s *Service) disconnectIsolated(ctx context.Context, c db.Customer) {
	dev := device.Radius{Q: s.Q, Key: s.Key}
	if err := dev.Disconnect(ctx, device.Customer{Username: c.Username}, ""); err != nil {
		slog.Error("isolir: disconnect after isolation ended", "customer", c.Username, "err", err)
	}
}

// EndIsolations ends every running isolation, for when isolir_enabled is switched off. Each affected
// customer's open sessions are dropped, so the next Access-Request gets the normal plan.
func (s *Service) EndIsolations(ctx context.Context) error {
	running, err := s.Q.ListRunningIsolations(ctx)
	if err != nil {
		return err
	}
	for _, iso := range running {
		if err := s.closeIsolation(ctx, iso, "disabled"); err != nil {
			return err
		}
	}
	return nil
}
