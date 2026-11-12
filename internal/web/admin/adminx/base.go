// Package adminx holds what the admin handler packages (network, reports, settings) share: the form and list
// builders, the CRUD helpers and the hooks into state that lives on the web Server. It imports no web package.
package adminx

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/frand-kod/gobill/internal/radius"
	"github.com/frand-kod/gobill/internal/service/provision"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/wg"
)

// Base is what every admin handler type embeds. The hooks are set by the web Server.
type Base struct {
	*core.Core
	*Shared
	Conn *sql.DB

	Deny         func(w http.ResponseWriter, r *http.Request, code int) // styled 403/404/405 page
	RadiusServer func() *radius.Server
	Prov         func() *provision.Service
	GobillAddr   func() (host, port string)
	// ProxyWarning is the admin warning for a reverse proxy while Trust Proxy is off, empty when there is none.
	ProxyWarning func(r *http.Request) string
	Now          func() time.Time
}

// Mw is the role middleware the web Server builds once per Handler call.
type Mw struct {
	All, Managers, Staff, NoReport, Money, SuperAdmin func(http.Handler) http.Handler
	// Require builds the middleware for any other role list.
	Require func(roles ...string) func(http.Handler) http.Handler
}

// Crud registers the six managers-only routes of a CRUD page under base.
func (m Mw) Crud(mux *http.ServeMux, base string, list, nw, edit, save, del http.HandlerFunc) {
	mux.Handle("GET "+base, m.Managers(list))
	mux.Handle("GET "+base+"/new", m.Managers(nw))
	mux.Handle("POST "+base, m.Managers(save))
	mux.Handle("GET "+base+"/{id}/edit", m.Managers(edit))
	mux.Handle("POST "+base+"/{id}", m.Managers(save))
	mux.Handle("POST "+base+"/{id}/delete", m.Managers(del))
}

// Shared is the Server state the admin packages read. The web Server embeds it so main can set the fields.
type Shared struct {
	// DBPath is the SQLite file: /health reports its disk space, restore stages uploads next to it.
	DBPath string
	// ListenAddr is GOBILL_HTTP, the address the MikroTik setup helper of the isolir tab names.
	ListenAddr string
	// WG is the built-in WireGuard tunnel; nil = none.
	WG *wg.Manager
	// ClockWarning, if set, returns a non-empty reason while the clock is untrusted.
	ClockWarning func() string
	// BackupMirror is the optional off-site backup folder (GOBILL_BACKUP_MIRROR), shown to SuperAdmin.
	BackupMirror string
}
