// Package customers serves the admin customer pages: list, detail, edit, actions, summary and custom fields.
package customers

import "github.com/frand-kod/gobill/internal/web/admin/adminx"

// Handlers serves the admin customer pages.
type Handlers struct {
	*adminx.Base
}

// New returns the customer handlers.
func New(b *adminx.Base) *Handlers { return &Handlers{Base: b} }
