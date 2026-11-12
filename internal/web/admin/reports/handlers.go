// Package reports serves the admin dashboard, reports, logs, payment gateway audit, status, search and metrics pages.
package reports

import (
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/admin/network"
	"github.com/frand-kod/gobill/internal/web/core"
)

// Handlers serves the pages of this package.
type Handlers struct {
	*adminx.Base
	net *network.Handlers
	// SettingsFields lists the fields of a settings sub-page for the search; web sets it.
	SettingsFields func(tab string) []adminx.Field
	// admin bell counts, cached for noticeCacheFor, see BellItems
	noticeMu    sync.Mutex
	noticeAt    time.Time
	noticeItems []core.AttnItem
}

// New returns the handlers; net supplies the network card of the dashboard and the bell.
func New(b *adminx.Base, net *network.Handlers) *Handlers { return &Handlers{Base: b, net: net} }
