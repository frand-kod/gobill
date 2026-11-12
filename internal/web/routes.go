package web

// Route registration: the main mux table and per-area route groups.

import (
	"io/fs"
	"net/http"

	"strings"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/service/apitoken"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/admin/apiaccess"
	"github.com/frand-kod/gobill/internal/web/admin/billing"
	"github.com/frand-kod/gobill/internal/web/admin/customers"
	"github.com/frand-kod/gobill/internal/web/admin/network"
	"github.com/frand-kod/gobill/internal/web/admin/plans"
	"github.com/frand-kod/gobill/internal/web/admin/reports"
	adminsettings "github.com/frand-kod/gobill/internal/web/admin/settings"
	"github.com/frand-kod/gobill/internal/web/api"
	"github.com/frand-kod/gobill/internal/web/portal"
	"github.com/frand-kod/gobill/internal/web/public"
	"github.com/frand-kod/gobill/internal/web/setup"
)

// prefetchSafe is the session middleware with one change: a prefetch (Sec-Purpose, or Purpose on older
// builds) reads the session but never saves it, so a flash or a one-time value (metrics_token_once) survives
// for the real navigation. Responses vary on Sec-Purpose so a cached prefetch is never served as a navigation.
// ponytail: a prefetched page can be a few minutes old when shown; acceptable for intent-based prefetch.
func (s *Server) prefetchSafe(next http.Handler) http.Handler {
	saved := s.Sessions.LoadAndSave(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Sec-Purpose")
		if !isPrefetch(r) {
			saved.ServeHTTP(w, r)
			return
		}
		token := ""
		if c, err := r.Cookie(s.Sessions.Cookie.Name); err == nil {
			token = c.Value
		}
		ctx, err := s.Sessions.Load(r.Context(), token)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isPrefetch(r *http.Request) bool {
	for _, h := range []string{r.Header.Get("Sec-Purpose"), r.Header.Get("Purpose")} {
		h = strings.ToLower(h)
		if strings.Contains(h, "prefetch") || strings.Contains(h, "prerender") {
			return true
		}
	}
	return false
}

// adminMw builds the role middleware once per Handler call.
func (s *Server) adminMw() adminx.Mw {
	all := s.requireAdmin()
	managers := s.requireAdmin("SuperAdmin", "Admin")
	// Old PHP: bandwidth, routers, pool and logs are SuperAdmin/Admin only; customers are
	// readable by everyone, creatable by Agent/Sales too, editable/deletable by managers.
	staff := s.requireAdmin("SuperAdmin", "Admin", "Agent", "Sales")
	// role_access_mode: strict narrows these to managers (money) or staff (reading customer data);
	// phpnuxbill keeps the old PHP rules.
	return adminx.Mw{All: all, Managers: managers, Staff: staff, NoReport: s.byMode(staff, all), Money: s.byMode(managers, staff),
		SuperAdmin: s.requireAdmin("SuperAdmin"), Require: s.requireAdmin}
}

// baseRoutes are the routes of the web package itself: root redirect, static files, uploads, admin login and
// logout, app manifests and the "Panduan" guides.
func (s *Server) baseRoutes(mux *http.ServeMux, all func(http.Handler) http.Handler, static fs.FS) {
	mux.HandleFunc("GET /{$}", s.root)
	mux.Handle("GET /static/", staticHandler(static))
	mux.HandleFunc("GET /admin/login", s.loginForm)
	mux.HandleFunc("GET /uploads/{name}", s.serveUpload)
	mux.HandleFunc("POST /admin/login", s.loginSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /admin/login/2fa", s.login2FAForm)
	mux.HandleFunc("POST /admin/login/2fa", s.login2FASubmit)
	// app manifests (public: the browser fetches them without cookies)
	mux.HandleFunc("GET /admin/manifest.webmanifest", s.manifest(" Admin", "/admin", "/admin/"))
	mux.HandleFunc("GET /portal/manifest.webmanifest", s.manifest("", "/portal", "/portal/"))
	// the "Panduan" guides, open to any logged-in admin
	mux.Handle("GET /admin/docs", all(http.HandlerFunc(s.docsIndex)))
	mux.Handle("GET /admin/docs/{slug}", all(http.HandlerFunc(s.docsPage)))
}

// Handler returns the router wrapped in CSRF protection and session loading.
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(gobill.FS, "web/static")
	if err != nil {
		panic(err) // the path is embedded at build time
	}
	mux := http.NewServeMux()
	mw := s.adminMw()
	network.Register(mux, s.net, mw)
	apiaccess.Register(mux, s.apiPage, mw)
	customers.Register(mux, s.customers, mw)
	plans.Register(mux, s.plans, mw)
	billing.Register(mux, s.billing, mw)
	reports.Register(mux, s.reports, mw)
	adminsettings.Register(mux, s.settings, mw)
	setup.Register(mux, s.setup, mw.SuperAdmin)
	mux.Handle("GET /admin/settings/isolir/preview", mw.SuperAdmin(http.HandlerFunc(s.public.IsolirPreview)))
	s.baseRoutes(mux, mw.All, static)
	portal.Register(mux, s.portal)
	api.Register(mux, api.New(s.Queries, &apitoken.Service{Q: s.Queries}, s.Settings))
	public.Register(mux, s.public)

	// the Tripay server posts the callback without our origin; its HMAC signature authenticates it
	cop := http.NewCrossOriginProtection()
	for _, p := range public.OriginBypass {
		cop.AddInsecureBypassPattern(p)
	}
	// isolirGate sits inside realIP (it needs the real client IP) and outside the session chain
	app := s.realIP(s.public.IsolirGate(cop.Handler(s.prefetchSafe(s.idleGuard(s.maintenance(s.styledNotFound(mux)))))))
	// /health sits outside the session and maintenance chain so monitors always get an answer
	top := http.NewServeMux()
	top.HandleFunc("GET /health", s.health)
	top.HandleFunc("GET /metrics", s.reports.MetricsHandler)
	top.HandleFunc("GET /licenses", licenses)
	top.Handle("/", app)
	return cacheDefaults(top)
}
