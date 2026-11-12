// Package settings serves the admin settings pages and the security paths next to them: admin users,
// two-factor setup, the encryption key, backup, restore, the PHPNuxBill import, promotions and static pages.
package settings

import (
	"time"

	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/admin/network"
)

// Handlers serves the pages of this package.
type Handlers struct {
	*adminx.Base
	net *network.Handlers

	testSent map[int64][]time.Time // admin id -> Integrations test times, see testAllow
	// TelegramAPI overrides the Telegram API root in tests (empty = the notify default)
	TelegramAPI string
}

// New returns the handlers.
func New(b *adminx.Base, net *network.Handlers) *Handlers { return &Handlers{Base: b, net: net} }
