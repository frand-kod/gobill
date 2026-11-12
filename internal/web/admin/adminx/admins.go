package adminx

import (
	"github.com/frand-kod/gobill/internal/service/admin"
	"github.com/frand-kod/gobill/internal/web/core"
)

// Admins is the admin service: saves, password rules and the legacy login.
func (s *Base) Admins() *admin.Service {
	return &admin.Service{Q: s.Queries, Cost: core.BcryptCost}
}
