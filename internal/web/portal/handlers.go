// Package portal serves the customer portal: login, dashboard, usage, orders, payments and profile.
// It shares sessions, templates and settings with the rest of the web layer through core.
package portal

import (
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/web/core"
)

// Handlers serves the customer portal.
type Handlers struct {
	*core.Core

	PayCheckMu sync.Mutex // guards PayCheckAt: last gateway status call per payment id, see PayCheckWait
	PayCheckAt map[int64]time.Time
}

// New returns the portal handlers on top of the shared core.
func New(c *core.Core) *Handlers { return &Handlers{Core: c} }
