// Package billing serves the admin money pages: recharge, deposit, vouchers, coupons, agent batches and promo cards.
package billing

import "github.com/frand-kod/gobill/internal/web/admin/adminx"

// Handlers serves the pages of this package.
type Handlers struct {
	*adminx.Base
}

// New returns the handlers.
func New(b *adminx.Base) *Handlers { return &Handlers{Base: b} }
