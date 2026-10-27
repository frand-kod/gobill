package billing

// Sync of customer, subscription and plan changes to routers.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"time"
)

// SyncPlan pushes an admin plan change to the plan's router. op is "add", "update" or
// "remove"; oldName is the plan's name before an update. Balance plans have no device.
func (s *Service) SyncPlan(ctx context.Context, op string, p db.Plan, oldName string) error {
	// a RADIUS plan needs no router: its online users get the CoA through the NAS table
	if p.Type == "Balance" || (!p.RouterID.Valid && p.Device != "Radius") {
		return nil
	}
	var router db.Router
	if p.RouterID.Valid {
		var err error
		if router, err = s.Q.GetRouter(ctx, p.RouterID.Int64); err != nil {
			return err
		}
	}
	mk := s.DeviceFor
	if mk == nil {
		mk = s.defaultDevice
	}
	dev, err := mk(p, router)
	if err != nil {
		return err
	}
	dp, err := s.devicePlan(ctx, p, true)
	if err != nil {
		return err
	}
	switch op {
	case "add":
		return dev.AddPlan(ctx, dp)
	case "update":
		return dev.UpdatePlan(ctx, oldName, dp)
	case "remove":
		return dev.RemovePlan(ctx, dp)
	}
	return fmt.Errorf("unknown plan sync op %q", op)
}

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

// ErrRadiusRouter: a router whose plans all use RADIUS has nothing to push.
var ErrRadiusRouter = errors.New("router is in RADIUS mode, nothing to push")

// SyncReport is the result of SyncRouter: counts and one "item: reason" line per failure.
type SyncReport struct {
	OK, Failed int
	Failures   []string
}

// SyncRouter ("Dorong semua") pushes every enabled plan and then every active subscription of an
// API-mode router, for a new, reset or replacement router. It is safe to run twice: plans go as
// "update" (which adds a missing profile) and AddCustomer updates an existing account. A router
// that does not answer a ping stops the run before any item is tried; item failures are
// collected and the loop continues. Runs inline: one API session per item, no job queue.
// ponytail: synchronous in the request, move to a background job if routers hold thousands of accounts.
func (s *Service) SyncRouter(ctx context.Context, routerID, adminID int64) (SyncReport, error) {
	var rep SyncReport
	r, err := s.Q.GetRouter(ctx, routerID)
	if err != nil {
		return rep, err
	}
	plans, err := s.Q.ListPlansByRouter(ctx, sql.NullInt64{Int64: routerID, Valid: true})
	if err != nil {
		return rep, err
	}
	api := plans[:0]
	for _, p := range plans {
		if p.Device != "Radius" {
			api = append(api, p)
		}
	}
	if len(plans) > 0 && len(api) == 0 {
		return rep, ErrRadiusRouter
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err = s.Ping(pctx, r)
	cancel()
	if err != nil {
		return rep, fmt.Errorf("router offline: %w", err)
	}
	note := func(what string, err error) {
		if err != nil {
			rep.Failed++
			rep.Failures = append(rep.Failures, what+": "+err.Error())
		} else {
			rep.OK++
		}
	}
	for _, p := range api {
		note("plan "+p.Name, s.SyncPlan(ctx, "update", p, p.Name))
	}
	ids, err := s.Q.ListActiveSubscriptionIDsByRouter(ctx, sql.NullInt64{Int64: routerID, Valid: true})
	if err != nil {
		return rep, err
	}
	for _, id := range ids {
		what := fmt.Sprint("subscription ", id)
		if sub, err := s.Q.GetSubscription(ctx, id); err == nil {
			if c, err := s.Q.GetCustomer(ctx, sub.CustomerID); err == nil {
				what = "customer " + c.Username
			}
		}
		note(what, s.SyncSubscription(ctx, id, adminID))
	}
	return rep, nil
}
