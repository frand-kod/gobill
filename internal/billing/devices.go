package billing

// Router device preparation, lookup and removal for plans; device failure tracking.

import (
	"log/slog"

	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
	"time"
)

func (s *Service) removeFromDevice(ctx context.Context, c db.Customer, p db.Plan) {
	dev, dc, dp, _, err := s.prepare(ctx, c, p)
	if err == nil {
		err = dev.RemoveCustomer(ctx, dc, dp)
	}
	if err != nil {
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
		return device.Radius{Q: s.Q, Key: s.Key}, nil
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
