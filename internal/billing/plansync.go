package billing

import (
	"context"
	"fmt"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

// SyncPlan pushes an admin plan change to the plan's router. op is "add", "update" or
// "remove"; oldName is the plan's name before an update. Balance plans have no device.
func (s *Service) SyncPlan(ctx context.Context, op string, p db.Plan, oldName string) error {
	if p.Type == "Balance" || !p.RouterID.Valid {
		return nil
	}
	router, err := s.Q.GetRouter(ctx, p.RouterID.Int64)
	if err != nil {
		return err
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
