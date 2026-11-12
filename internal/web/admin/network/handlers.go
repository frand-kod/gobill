// Package network serves the admin pages for routers, NAS, WireGuard, router migration, live RADIUS
// sessions, ODP and the maps. It shares sessions, templates and settings through adminx and core.
package network

import "github.com/frand-kod/gobill/internal/web/admin/adminx"

// Handlers serves the network pages.
type Handlers struct{ *adminx.Base }

// New returns the network handlers on top of the shared admin base.
func New(b *adminx.Base) *Handlers { return &Handlers{Base: b} }
