package billing

// Customer deactivate, delete and sync.

import (
	"context"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
)

// activeSubs lists the customer's subscriptions with status active.
func (s *Service) activeSubs(ctx context.Context, customerID int64) ([]db.Subscription, error) {
	subs, err := s.Q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: customerID, Limit: 500})
	var out []db.Subscription
	for _, x := range subs {
		if x.Status == "active" {
			out = append(out, x)
		}
	}
	return out, err
}

// DeactivateCustomer expires every active subscription now and removes it from its device (old
// customers/deactivate, for all plans). It keeps going after a device failure and returns the
// number deactivated plus the joined errors.
func (s *Service) DeactivateCustomer(ctx context.Context, customerID, adminID int64) (int, error) {
	subs, err := s.activeSubs(ctx, customerID)
	n := 0
	for _, x := range subs {
		if e := s.DeactivateSubscription(ctx, x.ID, adminID); e != nil {
			err = errors.Join(err, e)
		} else {
			n++
		}
	}
	return n, err
}

// DeleteCustomer removes the customer and its recharges but keeps transactions (customer_id becomes
// NULL), like the old customers/delete. The router is contacted after the commit: a device failure
// is returned as an error but the delete stays.
func (s *Service) DeleteCustomer(ctx context.Context, id int64) error {
	c, err := s.Q.GetCustomer(ctx, id)
	if err != nil {
		return err
	}
	subs, err := s.activeSubs(ctx, id)
	if err != nil {
		return err
	}
	plans := make([]db.Plan, 0, len(subs))
	for _, x := range subs {
		p, err := s.Q.GetPlan(ctx, x.PlanID)
		if err != nil {
			return err
		}
		plans = append(plans, p)
	}
	if err := s.tx(ctx, func(q *db.Queries) error {
		if err := q.DetachCustomerTransactions(ctx, nullID(id)); err != nil {
			return err
		}
		return q.DeleteCustomer(ctx, id)
	}); err != nil {
		return err
	}
	var derr error
	for _, p := range plans {
		dev, dc, dp, _, e := s.prepare(ctx, c, p)
		if e == nil {
			e = dev.RemoveCustomer(ctx, dc, dp)
		}
		if e != nil {
			derr = errors.Join(derr, fmt.Errorf("remove from router: %w", e))
		}
	}
	return derr
}

// SyncCustomer re-sends every active subscription of the customer to its device.
func (s *Service) SyncCustomer(ctx context.Context, customerID, adminID int64) (int, error) {
	subs, err := s.activeSubs(ctx, customerID)
	n := 0
	for _, x := range subs {
		if e := s.SyncSubscription(ctx, x.ID, adminID); e != nil {
			err = errors.Join(err, e)
		} else {
			n++
		}
	}
	return n, err
}
