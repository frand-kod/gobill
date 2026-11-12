// Package plans serves the admin service plan, bandwidth and IP pool pages.
package plans

import (
	"github.com/frand-kod/gobill/internal/service/plan"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
)

// Handlers serves the plan, bandwidth and pool pages.
type Handlers struct {
	*adminx.Base
	Plans *plan.Service
}

// New returns the handlers.
func New(b *adminx.Base, p *plan.Service) *Handlers { return &Handlers{Base: b, Plans: p} }
