package billing

import (
	"context"
	"errors"
	"fmt"

	"github.com/frand-kod/gobill/internal/db"
)

// routerName is the account name the device holds for the customer: PPPoE uses pppoe_username
// when set (device.pppUser), everything else the username.
func routerName(c db.Customer, planType string) string {
	if planType == "PPPoE" && c.PppoeUsername != "" {
		return c.PppoeUsername
	}
	return c.Username
}

// SyncAfterEdit pushes an edited customer (already saved) to the devices of every active
// subscription: the account is renamed on the router first when the username (or pppoe_username)
// changed, so the following add_customer updates it instead of creating a duplicate
// (customers.php:788-815). old is the customer as it was before the edit. Errors are joined; the
// loop continues after a device failure.
func (s *Service) SyncAfterEdit(ctx context.Context, old db.Customer, adminID int64) (int, error) {
	subs, err := s.activeSubs(ctx, old.ID)
	if err != nil {
		return 0, err
	}
	c, err := s.Q.GetCustomer(ctx, old.ID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sub := range subs {
		e := func() error {
			p, err := s.Q.GetPlan(ctx, sub.PlanID)
			if err != nil {
				return err
			}
			dev, dc, dp, _, err := s.prepare(ctx, c, p)
			if err != nil {
				return fmt.Errorf("prepare sync: %w", err)
			}
			if from, to := routerName(old, p.Type), routerName(c, p.Type); from != to {
				if err := dev.ChangeUsername(ctx, dp, from, to); err != nil {
					return fmt.Errorf("rename %s -> %s: %w", from, to, err)
				}
			}
			if err := dev.AddCustomer(ctx, dc, dp); err != nil {
				return err
			}
			return s.logAdmin(ctx, s.Q, adminID, "subscription.sync", fmt.Sprintf("%s %s", c.Username, p.Name))
		}()
		if e != nil {
			err = errors.Join(err, e)
		} else {
			n++
		}
	}
	return n, err
}

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
