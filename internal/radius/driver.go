package radius

import (
	"context"
	"log/slog"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
)

// Driver is the device.Device for plans served by the built-in RADIUS server: the router is not touched and
// the server reads customer and plan from the DB at auth time. Only online users need work:
// a Disconnect-Request (RFC 5176) to kick them, a CoA-Request when their plan changes.
type Driver struct {
	Q    *db.Queries
	Key  []byte
	Port string // default CoAPort
}

func (Driver) ChangeUsername(context.Context, device.Plan, string, string) error      { return nil }
func (Driver) AddPlan(context.Context, device.Plan) error                             { return nil }
func (Driver) RemovePlan(context.Context, device.Plan) error                          { return nil }
func (Driver) Connect(context.Context, device.Customer, string, string, string) error { return nil }

// AddCustomer (recharge, plan change, sync) pushes the new rate and expiry to the user's online
// sessions. It never fails: Access-Request reads them from the DB at the next login anyway.
func (r Driver) AddCustomer(ctx context.Context, c device.Customer, _ device.Plan) error {
	ss, err := r.Q.ListOpenRadiusSessionsByUser(ctx, c.Username)
	if err != nil {
		slog.Warn("radius: list sessions for CoA", "user", c.Username, "err", err)
	}
	r.coa(ctx, ss, "")
	return nil
}

// UpdatePlan (admin plan edit) pushes the plan's new rate to every online user on it.
func (r Driver) UpdatePlan(ctx context.Context, _ string, p device.Plan) error {
	ss, err := r.Q.SearchOpenRadiusSessions(ctx, db.SearchOpenRadiusSessionsParams{PageLimit: -1})
	if err != nil {
		return err
	}
	// ponytail: one GetRadiusLogin per open session to find the plan's users; a join query if this gets slow.
	r.coa(ctx, ss, p.Name)
	return nil
}

// coa sends a CoA-Request per session in the background, so the HTTP request that triggered it is
// not held by NAS timeouts. A NAK or timeout falls back to Disconnect-Request: the user reconnects
// and Access-Accept carries the new plan.
func (r Driver) coa(_ context.Context, ss []db.RadiusSession, plan string) {
	if len(ss) == 0 {
		return
	}
	go func() {
		// ponytail: sessions go one by one in one goroutine; fan out if a plan edit hits hundreds of dead NAS.
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(len(ss))*7*time.Second)
		defer cancel()
		for _, s := range ss {
			err := CoA(ctx, r.Q, r.Key, r.Port, s, plan, time.Now())
			if err == nil {
				continue
			}
			slog.Warn("radius: CoA failed, disconnecting", "user", s.Username, "nas", s.NasIp, "err", err)
			if err := Disconnect(ctx, r.Q, r.Key, r.Port, s); err != nil {
				slog.Warn("radius: disconnect after CoA failed", "user", s.Username, "nas", s.NasIp, "err", err)
			}
		}
	}()
}

func (r Driver) IsOnline(ctx context.Context, c device.Customer, _ string) (bool, error) {
	ss, err := r.Q.ListOpenRadiusSessionsByUser(ctx, c.Username)
	return len(ss) > 0, err
}

// RemoveCustomer (expiry) kicks the user but never fails: Access-Request already rejects an
// expired account, so an unreachable NAS must not keep the subscription active.
func (r Driver) RemoveCustomer(ctx context.Context, c device.Customer, _ device.Plan) error {
	if err := r.Disconnect(ctx, c, ""); err != nil {
		slog.Warn("radius: disconnect on expiry failed", "user", c.Username, "err", err)
	}
	return nil
}

func (r Driver) Disconnect(ctx context.Context, c device.Customer, _ string) error {
	ss, err := r.Q.ListOpenRadiusSessionsByUser(ctx, c.Username)
	if err != nil {
		return err
	}
	port := r.Port
	if port == "" {
		port = CoAPort
	}
	var first error
	for _, s := range ss {
		if err := Disconnect(ctx, r.Q, r.Key, port, s); err != nil && first == nil {
			first = err
		}
	}
	return first
}
