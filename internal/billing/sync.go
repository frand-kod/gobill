package billing

import (
	"context"
	"errors"
	"fmt"
)

// SyncSubscription re-sends an active subscription's customer and plan to its device (old
// subscription sync). Like the other router actions, a device failure is returned to the caller.
func (s *Service) SyncSubscription(ctx context.Context, id, adminID int64) error {
	sub, err := s.Q.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if sub.Status != "active" {
		return errors.New("subscription is not active")
	}
	c, err := s.Q.GetCustomer(ctx, sub.CustomerID)
	if err != nil {
		return err
	}
	p, err := s.Q.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return err
	}
	dev, dc, dp, _, err := s.prepare(ctx, c, p)
	if err != nil {
		return fmt.Errorf("prepare sync: %w", err)
	}
	if err := dev.AddCustomer(ctx, dc, dp); err != nil {
		return err
	}
	return s.logAdmin(ctx, s.Q, adminID, "subscription.sync", fmt.Sprintf("%s %s", c.Username, p.Name))
}
