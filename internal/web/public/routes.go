package public

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

// Register adds the public routes.
func Register(mux *http.ServeMux, h *Handlers) {
	mux.HandleFunc("GET /pages/{slug}", h.pagePublic)
	mux.HandleFunc("POST /callback/tripay", h.tripayCallback)
	mux.HandleFunc("POST /callback/duitku", h.tripayCallback) // same handler: the configured gateway verifies it
	mux.HandleFunc("GET /qris/{id}/{token}", h.qrisPage)

	// RADIUS REST endpoint of old phpnuxbill (radius.php): an existing FreeRADIUS only needs a new connect_uri
	mux.HandleFunc("POST /radius.php", h.radiusRest)
	mux.HandleFunc("POST /radius/rest", h.radiusRest)
	if m, err := h.LoadSettings(context.Background()); err == nil && strings.TrimSpace(m["radius_rest_allow"]) == "" && m["radius_rest_secret"] == "" {
		slog.Warn("radius.php endpoint refuses ALL requests: neither radius_rest_allow nor radius_rest_secret is set. Before, an empty allow-list meant loopback only. If FreeRADIUS uses this endpoint, set radius_rest_allow (e.g. 127.0.0.1 or its IP) or radius_rest_secret (Settings > System > FreeRADIUS), see docs/en/freeradius-rest.md")
	}

	// the router fetches its provisioning script here; the one-time token is the credential
	mux.HandleFunc("GET /provision/{token}", h.provisionScript)

	// ISOLIR page for expired customers: public, identified by client IP
	mux.HandleFunc("GET /isolir", h.isolirPage)
	mux.HandleFunc("POST /isolir/voucher", h.isolirVoucher)
}

// OriginBypass are the POST routes that skip the origin check: the gateway callbacks (authenticated by
// their signature), the RADIUS REST endpoint (no browser) and the isolir voucher form, which posts from a
// sandboxed page with origin null.
var OriginBypass = []string{"POST /callback/tripay", "POST /callback/duitku", "POST /isolir/voucher", "POST /radius.php", "POST /radius/rest"}
