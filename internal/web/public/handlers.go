// Package public serves the endpoints that need no login and are neither admin nor customer portal:
// the isolir page, payment gateway callbacks, the FreeRADIUS REST endpoint, router provisioning,
// static pages and the amount-locked QRIS page. It shares sessions, templates and settings through core.
// What lives in web (the RADIUS server, the WireGuard setup) comes in as function fields.
package public

import (
	"context"
	"net/http"

	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/service/provision"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/wg"
)

// Handlers serves the public endpoints.
type Handlers struct {
	*core.Core

	// IsolirOn reports the isolir_enabled setting; the gate does no query while it is false.
	IsolirOn func() bool
	// RadiusServer is the shared RADIUS decision engine behind /radius.php.
	RadiusServer func() *radius.Server
	// OriginalPeer is the TCP peer address before the real-IP middleware rewrote RemoteAddr.
	OriginalPeer func(r *http.Request) string
	// WGUsable gives the WireGuard config and settings, or an error while the tunnel cannot run.
	WGUsable func(ctx context.Context) (wg.Config, map[string]string, error)
	// WGEndpoint is the tunnel endpoint the provisioning script names, with defaults applied.
	WGEndpoint func(st map[string]string) string
	// Prov is the provisioning service, built per call because the secret key is set after New.
	Prov func() *provision.Service
	// Prender renders a page of the customer layout.
	Prender func(w http.ResponseWriter, r *http.Request, status int, name string, p core.Page)
}

// New returns the public handlers on top of the shared core.
func New(c *core.Core) *Handlers { return &Handlers{Core: c} }
