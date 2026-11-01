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

// SuspendCustomer takes every active subscription of the customer off its device while the DB rows
// stay active (status Banned/Disabled/Suspended); SyncCustomer puts them back. A RADIUS device also
// drops the open sessions. It keeps going after a failure and returns the joined errors.
func (s *Service) SuspendCustomer(ctx context.Context, customerID int64) (int, error) {
	c, err := s.Q.GetCustomer(ctx, customerID)
	if err != nil {
		return 0, err
	}
	subs, err := s.activeSubs(ctx, customerID)
	n := 0
	for _, x := range subs {
		p, e := s.Q.GetPlan(ctx, x.PlanID)
		if e == nil {
			e = s.removeOrQueue(ctx, c, p, "suspend")
		}
		if e != nil {
			err = errors.Join(err, e)
		} else {
			n++
		}
	}
	return n, err
}

// DeleteCustomer removes the customer from its router(s) first and deletes the DB row (and its
// recharges; transactions and payment requests are kept with customer_id NULL, like the old
// customers/delete) only when that worked. A device failure refuses the delete and keeps the row.
func (s *Service) DeleteCustomer(ctx context.Context, id int64) error { return s.DeleteCustomerForce(ctx, id, false) }

// DeleteCustomerForce is DeleteCustomer; with force the row is deleted even when the router removal
// fails (the router error is still returned, the delete stays).
func (s *Service) DeleteCustomerForce(ctx context.Context, id int64, force bool) error {
	c, err := s.Q.GetCustomer(ctx, id)
	if err != nil {
		return err
	}
	subs, err := s.activeSubs(ctx, id)
	if err != nil {
		return err
	}
	var derr error
	for _, x := range subs {
		p, err := s.Q.GetPlan(ctx, x.PlanID)
		if err != nil {
			return err
		}
		dev, dc, dp, _, e := s.prepare(ctx, c, p)
		if e == nil {
			dp.ExpiredPlan = nil // a deleted customer leaves the router entirely (PHP customers/delete)
			e = dev.RemoveCustomer(ctx, dc, dp)
		}
		if e != nil {
			derr = errors.Join(derr, fmt.Errorf("remove from router: %w", e))
		}
	}
	if derr != nil && !force {
		return derr
	}
	if err := s.tx(ctx, func(q *db.Queries) error {
		if err := q.DetachCustomerTransactions(ctx, nullID(id)); err != nil {
			return err
		}
		return q.DeleteCustomer(ctx, id)
	}); err != nil {
		return err
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
