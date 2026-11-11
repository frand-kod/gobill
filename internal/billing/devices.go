package billing

// Router device preparation, lookup and removal for plans; device failure tracking.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/secret"
	"time"
)

// removeNow takes the customer off the plan's device. kind "delete" removes the account entirely
// (no isolation plan); every other kind lets the device move it to its expired plan.
func (s *Service) removeNow(ctx context.Context, c db.Customer, p db.Plan, kind string) error {
	dev, dc, dp, _, err := s.prepare(ctx, c, p)
	if err == nil {
		if kind == "delete" {
			dp.ExpiredPlan = nil
		}
		err = dev.RemoveCustomer(ctx, dc, dp)
	}
	return err
}

// removeOrQueue is removeNow; a failure on a router plan is also written to router_pending, so the
// retry job finishes the removal when the router is back. The error is still returned.
func (s *Service) removeOrQueue(ctx context.Context, c db.Customer, p db.Plan, kind string) error {
	err := s.removeNow(ctx, c, p, kind)
	if err != nil {
		err = errors.Join(err, s.queueRemoval(ctx, c, p, kind, err))
	}
	return err
}

type pendingPayload struct {
	PlanID int64  `json:"plan_id"`
	PPPoE  string `json:"pppoe_username,omitempty"`
}

// queueRemoval records a removal that did not happen. Plans without a router (RADIUS, dummy) have
// no outage to wait for and are not queued. It returns only a failure to record.
func (s *Service) queueRemoval(ctx context.Context, c db.Customer, p db.Plan, kind string, cause error) error {
	if !p.RouterID.Valid {
		return nil
	}
	b, _ := json.Marshal(pendingPayload{PlanID: p.ID, PPPoE: c.PppoeUsername})
	return s.Q.AddRouterPending(ctx, db.AddRouterPendingParams{RouterID: p.RouterID.Int64, Kind: kind, Username: c.Username,
		Payload: string(b), LastError: cause.Error(), CreatedAt: s.now().Unix()})
}

// pendingBackoff is the wait after the n-th failed retry: 5 min, doubling, capped at about 5 hours.
func pendingBackoff(attempts int64) time.Duration {
	return 5 * time.Minute << min(attempts, 6)
}

// RetryPending retries the queued router removals that are due. A router that fails once is not
// dialed again for the rest of the run. An entry is dropped when it succeeds, when its plan is gone,
// or when the customer is meant to be on the router again (active and Active subscription of the plan).
func (s *Service) RetryPending(ctx context.Context) error {
	rows, err := s.Q.ListRouterPending(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	dead := map[int64]bool{}
	for _, r := range rows {
		if dead[r.RouterID] || r.TriedAt+int64(pendingBackoff(r.Attempts)/time.Second) > now.Unix() {
			continue
		}
		done, err := s.retryOne(ctx, r)
		if err == nil && done {
			err = s.Q.DeleteRouterPending(ctx, r.ID)
		} else if err != nil {
			dead[r.RouterID] = true
			err = s.Q.BumpRouterPending(ctx, db.BumpRouterPendingParams{LastError: err.Error(), TriedAt: now.Unix(), ID: r.ID})
		}
		if err != nil {
			slog.Error("router pending", "id", r.ID, "err", err)
		}
	}
	return nil
}

// retryOne returns done=true when the entry needs no further work.
func (s *Service) retryOne(ctx context.Context, r db.RouterPending) (bool, error) {
	var pl pendingPayload
	if err := json.Unmarshal([]byte(r.Payload), &pl); err != nil {
		return true, nil
	}
	p, err := s.Q.GetPlan(ctx, pl.PlanID)
	if err != nil {
		return errors.Is(err, sql.ErrNoRows), err
	}
	c := db.Customer{Username: r.Username, PppoeUsername: pl.PPPoE}
	if cur, err := s.Q.GetCustomerByUsername(ctx, r.Username); err == nil {
		if cur.Status == "Active" {
			if _, live, _ := activeSub(ctx, s.Q, cur.ID, p.RouterID, p.Type); live {
				return true, nil
			}
		}
	}
	return true, s.removeNow(ctx, c, p, r.Kind)
}

func (s *Service) removeFromDevice(ctx context.Context, c db.Customer, p db.Plan) {
	if err := s.removeOrQueue(ctx, c, p, "remove"); err != nil {
		slog.Error("device: remove failed, sync manually", "customer", c.Username, "plan", p.Name, "err", err)
	}
}

type deviceFailKey struct{}

// TrackDeviceFailure returns a ctx and a flag that apply sets when the router call after a
// committed recharge failed. The recharge itself still succeeds (money and transaction are kept);
// the caller uses the flag to warn the operator to Sync.
func TrackDeviceFailure(ctx context.Context) (context.Context, *bool) {
	f := new(bool)
	return context.WithValue(ctx, deviceFailKey{}, f), f
}

// prepare resolves the driver and the device-side customer/plan for a plan's router.
func (s *Service) prepare(ctx context.Context, c db.Customer, p db.Plan) (dev device.Device, dc device.Customer, dp device.Plan, routerName string, err error) {
	var router db.Router // zero for Radius plans, whose device ignores it
	if p.RouterID.Valid {
		if router, err = s.Q.GetRouter(ctx, p.RouterID.Int64); err != nil {
			return
		}
	}
	mk := s.DeviceFor
	if mk == nil {
		mk = s.defaultDevice
	}
	if dev, err = mk(p, router); err != nil {
		return
	}
	dc = device.Customer{Username: c.Username, FullName: c.Fullname, Email: c.Email, Bills: []string{p.Name},
		PPPoEUsername: c.PppoeUsername, PPPoEIP: c.PppoeIp}
	if len(c.SecretEnc) > 0 {
		var pw []byte
		if pw, err = secret.Open(s.Key, c.SecretEnc); err != nil {
			err = fmt.Errorf("decrypt customer secret: %w", err)
			return
		}
		dc.Password = string(pw)
	}
	dp, err = s.devicePlan(ctx, p, true)
	return dev, dc, dp, router.Name, err
}

// CustomerOnline asks the plan's device whether the customer is connected (old check_customer_online).
// ponytail: one device call per page view, no cache; the 5s cap matches the old socket timeout.
func (s *Service) CustomerOnline(ctx context.Context, c db.Customer, p db.Plan) (bool, error) {
	dev, dc, _, router, err := s.prepare(ctx, c, p)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return dev.IsOnline(ctx, dc, router)
}

func (s *Service) devicePlan(ctx context.Context, p db.Plan, withExpired bool) (device.Plan, error) {
	dp := device.Plan{Name: p.Name, Type: p.Type, SharedUsers: int(p.SharedUsers.Int64), Limited: p.Limited == 1,
		LimitType: p.LimitType.String, TimeLimit: int(p.TimeLimit.Int64), TimeUnit: p.TimeUnit.String,
		DataLimit: int(p.DataLimit.Int64), DataUnit: p.DataUnit.String, OnLogin: p.OnLogin, OnLogout: p.OnLogout}
	if p.BandwidthID.Valid {
		b, err := s.Q.GetBandwidth(ctx, p.BandwidthID.Int64)
		if err != nil {
			return dp, err
		}
		dp.RateUp, dp.RateUpUnit, dp.RateDown, dp.RateDownUnit, dp.Burst = int(b.RateUp), b.RateUpUnit, int(b.RateDown), b.RateDownUnit, b.Burst
	}
	if p.PoolID.Valid {
		pl, err := s.Q.GetPool(ctx, p.PoolID.Int64)
		if err != nil {
			return dp, err
		}
		dp.Pool, dp.PoolLocalIP = pl.Name, pl.LocalIp
	}
	if withExpired && p.ExpiredPlanID.Valid {
		ep, err := s.Q.GetPlan(ctx, p.ExpiredPlanID.Int64)
		if err != nil {
			return dp, err
		}
		e, err := s.devicePlan(ctx, ep, false)
		if err != nil {
			return dp, err
		}
		dp.ExpiredPlan = &e
	}
	return dp, nil
}

func (s *Service) defaultDevice(p db.Plan, r db.Router) (device.Device, error) {
	if p.Device == "" || p.Device == "Dummy" {
		return device.Dummy{}, nil
	}
	if p.Device == "Radius" {
		return radius.Driver{Q: s.Q, Key: s.Key}, nil
	}
	rt, err := s.routerConn(r)
	if err != nil {
		return nil, err
	}
	switch p.Device {
	case "MikrotikHotspot":
		return device.NewMikrotikHotspot(rt), nil
	case "MikrotikPppoe":
		return device.NewMikrotikPPPoE(rt), nil
	}
	return nil, fmt.Errorf("unknown device %q", p.Device)
}
