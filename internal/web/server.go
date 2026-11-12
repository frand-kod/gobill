// Package web holds the HTTP server: routes, middleware, handlers and templates.
package web

// Server type, construction, shared accessors and small helpers.

import (
	"database/sql"
	"net/http"
	"net/netip"
	"sync/atomic"

	"github.com/frand-kod/gobill/internal/service/apitoken"
	"github.com/frand-kod/gobill/internal/service/customer"
	"github.com/frand-kod/gobill/internal/service/plan"
	"github.com/frand-kod/gobill/internal/settings"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/admin/apiaccess"
	"github.com/frand-kod/gobill/internal/web/admin/billing"
	"github.com/frand-kod/gobill/internal/web/admin/customers"
	"github.com/frand-kod/gobill/internal/web/admin/network"
	"github.com/frand-kod/gobill/internal/web/admin/plans"
	"github.com/frand-kod/gobill/internal/web/admin/reports"
	adminsettings "github.com/frand-kod/gobill/internal/web/admin/settings"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/portal"
	"github.com/frand-kod/gobill/internal/web/public"
	"github.com/frand-kod/gobill/internal/web/setup"

	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/i18n"
	"github.com/frand-kod/gobill/internal/radius"
	"golang.org/x/crypto/bcrypt"
)

// Server wires the database, sessions, translations and templates together.
type Server struct {
	core.Core
	adminx.Shared
	ax        *adminx.Base
	portal    *portal.Handlers
	public    *public.Handlers
	setup     *setup.Handlers
	net       *network.Handlers
	apiPage   *apiaccess.Handlers
	customers *customers.Handlers
	billing   *billing.Handlers
	plans     *plans.Handlers
	reports   *reports.Handlers
	settings  *adminsettings.Handlers
	conn      *sql.DB
	guides    map[string]*guideSet // rendered "Panduan" pages per language ("id", "en"), see docs.go

	// Radius is shared with the UDP listener so auth throttles apply to /radius.php too.
	Radius     *radius.Server
	radiusOnce sync.Once

	payCheckMu sync.Mutex // guards payCheckAt: last gateway status call per payment id, see payCheckAllowed
	payCheckAt map[int64]time.Time

	// UpdateInfo returns the newest known release tag and its page, see job.UpdateCheck.
	UpdateInfo func() (tag, link string)
	Plans      *plan.Service

	// admin bell counts, cached for noticeCacheFor, see bellItems
	noticeMu    sync.Mutex
	noticeAt    time.Time
	clock       func() time.Time // tests set it; nil is the wall clock, see now
	noticeItems []attnItem

	idle      atomic.Int64 // admin idle timeout in ns, see ReloadSessionSettings
	single    atomic.Bool  // single_session
	isolir    atomic.Bool  // isolir_enabled, see isolirGate
	trust     atomic.Bool  // trust_proxy, see realIP
	proxyHint atomic.Int64 // unix time a loopback request carried proxy headers while trust_proxy=no, see noteProxyHeaders
	proxies   atomic.Value // []netip.Prefix from trusted_proxies, see realIP
}

// Page is the data every template receives; the portal packages share it through core.
type Page = core.Page

type (
	formHelp = core.FormHelp
	attnItem = core.AttnItem
	bellItem = core.BellItem
)

type ctxKey = core.AdminKey

// intro is core.Intro for the intro tables, whose literals are unkeyed.
type intro struct{ Title, Text, Tip string }

func New(conn *sql.DB, secureCookie bool) (*Server, error) {
	store := sqlite3store.New(conn)
	sm := scs.New()
	sm.Store = store
	sm.Lifetime = 12 * time.Hour
	// idle timeout is enforced by idleGuard so it can change at runtime
	sm.Cookie.Name = "gobill_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secureCookie

	catalog, err := i18n.Load(gobill.FS, "lang")
	if err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), core.BcryptCost)
	if err != nil {
		return nil, err
	}

	s := &Server{
		conn: conn,
	}
	s.Queries, s.Sessions, s.Catalog, s.DummyHash = db.NewCached(conn), sm, catalog, dummy
	s.Failed = map[string][]time.Time{}
	s.Settings = settings.New(s.Queries)
	s.Customers = &customer.Service{DB: conn, Q: s.Queries}
	s.Plans = &plan.Service{DB: conn, Q: s.Queries}
	st, err := s.LoadSettings(context.Background())
	if err != nil {
		return nil, err
	}
	s.Lang.Store(st["language"])
	s.ReloadSessionSettings(context.Background())
	s.Asset, s.Decorate = s.asset, s.decorate
	s.portal = portal.New(&s.Core)
	s.ax = &adminx.Base{Core: &s.Core, Shared: &s.Shared, Conn: conn, Deny: s.deny, RadiusServer: s.radiusServer, Prov: s.prov, Now: s.now, ProxyWarning: s.proxyWarning, ReloadSessionSettings: s.ReloadSessionSettings}
	s.net = network.New(s.ax)
	s.apiPage = apiaccess.New(s.ax, &apitoken.Service{Q: s.Queries})
	s.customers = customers.New(s.ax)
	s.billing = billing.New(s.ax)
	s.plans = plans.New(s.ax, s.Plans)
	s.reports = reports.New(s.ax, s.net)
	s.settings = adminsettings.New(s.ax, s.net)
	s.reports.SettingsFields = func(tab string) []adminx.Field { return s.settings.SettingsFields(tab, nil, nil, nil) }
	s.setup = &setup.Handlers{Base: s.ax, NetH: s.net, PlansH: s.plans, BillingH: s.billing, SettingsH: s.settings, PlanSvc: s.Plans}
	s.public = public.New(&s.Core)
	s.public.IsolirOn, s.public.RadiusServer, s.public.OriginalPeer = s.isolir.Load, s.radiusServer, originalPeer
	s.public.WGUsable, s.public.WGEndpoint, s.public.Prov = s.net.WGUsable, func(st map[string]string) string { return network.WGDefaults(st)["wg_endpoint"] }, s.prov
	s.public.Prender = s.portal.Prender
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	if s.guides, err = loadAllGuides(gobill.Docs); err != nil {
		return nil, err
	}
	return s, nil
}

// now is the current time; tests set s.clock to fix it.
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// realIP replaces RemoteAddr with the rightmost X-Forwarded-For entry (added by our own proxy) when
// trust_proxy=yes and the TCP peer is loopback or in trusted_proxies. The TCP peer is kept in the
// context, see originalPeer.
func (s *Server) realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer := r.RemoteAddr
		r = r.WithContext(context.WithValue(r.Context(), peerKey{}, peer))
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" && s.trust.Load() && s.fromProxy(peer) {
			r.RemoteAddr = strings.TrimSpace(xf[strings.LastIndexByte(xf, ',')+1:])
		} else if !s.trust.Load() {
			s.noteProxyHeaders(r, peer)
		}
		next.ServeHTTP(w, r)
	})
}

// noteProxyHeaders records a loopback request that carries proxy headers while trust_proxy=no: a reverse
// proxy on this host whose clients all look like 127.0.0.1. It logs once per process, the status page
// and Quick Setup keep showing proxyWarning.
func (s *Server) noteProxyHeaders(r *http.Request, peer string) {
	if a, ok := httpx.PeerAddr(peer); !ok || !a.IsLoopback() {
		return
	}
	for _, h := range []string{"CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if r.Header.Get(h) != "" {
			if s.proxyHint.Swap(time.Now().Unix()) == 0 {
				slog.Warn("proxy headers from loopback but trust_proxy=no: every client shows as 127.0.0.1; set trust_proxy=yes", "header", h)
			}
			return
		}
	}
}

// proxyWarning is the admin warning for noteProxyHeaders, empty when there is nothing to warn about.
func (s *Server) proxyWarning(r *http.Request) string {
	if s.proxyHint.Load() == 0 || s.trust.Load() {
		return ""
	}
	return s.T("Requests arrive through a reverse proxy but Trust Proxy is off. All visitors share one IP: one attacker can lock every login, and the audit log loses real IPs. Set Trust Proxy to yes in Settings.")
}

// fromProxy reports whether the TCP peer may set X-Forwarded-For: loopback, or inside trusted_proxies.
func (s *Server) fromProxy(peer string) bool {
	a, ok := httpx.PeerAddr(peer)
	if !ok {
		return false
	}
	if a.IsLoopback() {
		return true
	}
	ps, _ := s.proxies.Load().([]netip.Prefix)
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// originalPeer is the TCP peer address, before realIP rewrote RemoteAddr.
func originalPeer(r *http.Request) string {
	if p, ok := r.Context().Value(peerKey{}).(string); ok {
		return p
	}
	return r.RemoteAddr
}

// parseTrustedProxies parses a comma-separated list of IPs and CIDRs. ok is false if any entry is invalid.
func parseTrustedProxies(spec string) ([]netip.Prefix, bool) {
	return settings.ParseTrustedProxies(spec)
}

type peerKey struct{}

// Gateway is what the payment flows need from an online gateway (Tripay, Duitku).
type Gateway = core.Gateway

// SweepUploads is the job body that removes abandoned import and restore uploads, see web/admin/settings.
func (s *Server) SweepUploads(ctx context.Context) error { return s.settings.SweepUploads(ctx) }
