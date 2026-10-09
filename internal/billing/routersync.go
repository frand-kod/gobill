package billing

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/device"
	"github.com/frand-kod/gobill/internal/secret"
)

// routerConn builds the connection info of a stored router; RouterFor overrides it in tests.
func (s *Service) routerConn(r db.Router) (device.Router, error) {
	if s.RouterFor != nil {
		return s.RouterFor(r)
	}
	pw, err := secret.Open(s.Key, r.PasswordEnc)
	if err != nil {
		return device.Router{}, fmt.Errorf("decrypt router password: %w", err)
	}
	return device.Router{Addr: net.JoinHostPort(r.Host, strconv.FormatInt(r.Port, 10)), User: r.Username, Pass: string(pw), TLS: r.Port == 8729}, nil
}

// Ping connects to the router and returns its identity.
func (s *Service) Ping(ctx context.Context, r db.Router) (string, error) {
	rt, err := s.routerConn(r)
	if err != nil {
		return "", err
	}
	return rt.Ping(ctx)
}

// SyncPool pushes an admin pool change to the pool's router (op: add, update, remove).
func (s *Service) SyncPool(ctx context.Context, op string, p db.Pool, oldName string) error {
	r, err := s.Q.GetRouter(ctx, p.RouterID)
	if err != nil {
		return err
	}
	rt, err := s.routerConn(r)
	if err != nil {
		return err
	}
	return rt.SyncPool(ctx, op, oldName, device.Pool{Name: p.Name, Ranges: p.RangeIp})
}
