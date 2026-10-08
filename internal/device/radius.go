package device

import (
	"context"
	"log/slog"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/radius"
)

// Radius is for plans served by the built-in RADIUS server: the router is not touched and
// the server reads customer and plan from the DB at auth time. Only kicking an online user
// needs work: a Disconnect-Request (RFC 5176) to each NAS holding an open session.
type Radius struct {
	Q    *db.Queries
	Key  []byte
	Port string // default radius.CoAPort
}

func (Radius) AddCustomer(context.Context, Customer, Plan) error               { return nil }
func (Radius) ChangeUsername(context.Context, Plan, string, string) error      { return nil }
func (Radius) AddPlan(context.Context, Plan) error                             { return nil }
func (Radius) UpdatePlan(context.Context, string, Plan) error                  { return nil }
func (Radius) RemovePlan(context.Context, Plan) error                          { return nil }
func (Radius) Connect(context.Context, Customer, string, string, string) error { return nil }

func (r Radius) IsOnline(ctx context.Context, c Customer, _ string) (bool, error) {
	ss, err := r.Q.ListOpenRadiusSessionsByUser(ctx, c.Username)
	return len(ss) > 0, err
}

// RemoveCustomer (expiry) kicks the user but never fails: Access-Request already rejects an
// expired account, so an unreachable NAS must not keep the subscription active.
func (r Radius) RemoveCustomer(ctx context.Context, c Customer, _ Plan) error {
	if err := r.Disconnect(ctx, c, ""); err != nil {
		slog.Warn("radius: disconnect on expiry failed", "user", c.Username, "err", err)
	}
	return nil
}

func (r Radius) Disconnect(ctx context.Context, c Customer, _ string) error {
	ss, err := r.Q.ListOpenRadiusSessionsByUser(ctx, c.Username)
	if err != nil {
		return err
	}
	port := r.Port
	if port == "" {
		port = radius.CoAPort
	}
	var first error
	for _, s := range ss {
		if err := radius.Disconnect(ctx, r.Q, r.Key, port, s); err != nil && first == nil {
			first = err
		}
	}
	return first
}
