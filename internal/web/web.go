// Package web holds the HTTP server: routes, middleware, handlers and templates.
package web

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
	"golang.org/x/crypto/bcrypt"

	nuxbill "github.com/frand-kod/nuxbill-go"
	"github.com/frand-kod/nuxbill-go/internal/billing"
	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/i18n"
)

const (
	maxFailedLogins = 10
	loginWindow     = 15 * time.Minute
)

// Server wires the database, sessions, translations and templates together.
type Server struct {
	conn      *sql.DB
	queries   *db.Queries
	sessions  *scs.SessionManager
	catalog   i18n.Catalog
	templates map[string]*template.Template
	lang      atomic.Value // string: current language, a global app setting
	dummyHash []byte       // compared against when the username does not exist

	// ClockWarning, if set, returns a non-empty reason while the clock is untrusted.
	ClockWarning func() string
	// SecretKey encrypts router passwords and customer secrets (see package secret).
	SecretKey       []byte
	SettingsChanged func(ctx context.Context) // called after settings are saved
	// Billing recharges customers and syncs plans to routers; nil disables both.
	Billing *billing.Service
	// CoAPort is the NAS Disconnect-Request port; empty = 3799.
	CoAPort string

	// ponytail: in-memory limiter, resets on restart; persist if needed
	mu     sync.Mutex
	failed map[string][]time.Time // client IP -> times of recent failed logins
}

// Page is the data every template receives.
type Page struct {
	Title    string
	Admin    *db.Admin
	Customer *db.Customer // portal customer, if any
	Flash    string
	Error    string
	Path     string
	Tabs     []option // settings sub-page menu
	Dir      string   // "rtl" or "ltr"
	Lang     string
	Data     any
}

type ctxKey struct{}

func New(conn *sql.DB, secureCookie bool) (*Server, error) {
	store := sqlite3store.New(conn)
	sm := scs.New()
	sm.Store = store
	sm.Lifetime = 12 * time.Hour
	sm.IdleTimeout = 2 * time.Hour
	sm.Cookie.Name = "nuxbill_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secureCookie

	catalog, err := i18n.Load(nuxbill.FS, "lang")
	if err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	s := &Server{
		conn:      conn,
		queries:   db.New(conn),
		sessions:  sm,
		catalog:   catalog,
		dummyHash: dummy,
		failed:    map[string][]time.Time{},
	}
	settings, err := s.loadSettings(context.Background())
	if err != nil {
		return nil, err
	}
	s.lang.Store(settings["language"])
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the router wrapped in CSRF protection and session loading.
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(nuxbill.FS, "web/static")
	if err != nil {
		panic(err) // the path is embedded at build time
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	all := s.requireAdmin()
	managers := s.requireAdmin("SuperAdmin", "Admin")
	mux.Handle("GET /admin", all(http.HandlerFunc(s.dashboard)))
	mux.Handle("GET /admin/settings", managers(http.HandlerFunc(s.settingsForm)))
	mux.Handle("GET /admin/settings/{tab}", managers(http.HandlerFunc(s.settingsForm)))
	mux.Handle("POST /admin/settings/{tab}", managers(http.HandlerFunc(s.settingsSave)))

	// Old PHP: bandwidth, routers, pool and logs are SuperAdmin/Admin only; customers are
	// readable by everyone, creatable by Agent/Sales too, editable/deletable by managers.
	staff := s.requireAdmin("SuperAdmin", "Admin", "Agent", "Sales")
	crud := func(base string, list, nw, edit, save, del http.HandlerFunc) {
		mux.Handle("GET "+base, managers(list))
		mux.Handle("GET "+base+"/new", managers(nw))
		mux.Handle("POST "+base, managers(save))
		mux.Handle("GET "+base+"/{id}/edit", managers(edit))
		mux.Handle("POST "+base+"/{id}", managers(save))
		mux.Handle("POST "+base+"/{id}/delete", managers(del))
	}
	crud("/admin/bandwidth", s.bwList, s.bwNew, s.bwEdit, s.bwSave, s.bwDelete)
	crud("/admin/routers", s.routerList, s.routerNew, s.routerEdit, s.routerSave, s.routerDelete)
	mux.Handle("POST /admin/routers/{id}/test", managers(http.HandlerFunc(s.routerTest)))
	crud("/admin/nas", s.nasList, s.nasNew, s.nasEdit, s.nasSave, s.nasDelete)
	mux.Handle("GET /admin/radius/sessions", managers(http.HandlerFunc(s.radiusSessions)))
	mux.Handle("POST /admin/radius/sessions/{id}/disconnect", managers(http.HandlerFunc(s.radiusDisconnect)))
	crud("/admin/pool", s.poolList, s.poolNew, s.poolEdit, s.poolSave, s.poolDelete)
	crud("/admin/plans", s.planList, s.planNew, s.planEdit, s.planSave, s.planDelete)
	mux.Handle("GET /admin/logs", managers(http.HandlerFunc(s.logList)))
	mux.Handle("GET /admin/customers", all(http.HandlerFunc(s.custList)))
	mux.Handle("GET /admin/customers/new", staff(http.HandlerFunc(s.custNew)))
	mux.Handle("POST /admin/customers", staff(http.HandlerFunc(s.custSave)))
	mux.Handle("GET /admin/customers/{id}", all(http.HandlerFunc(s.custView)))
	mux.Handle("GET /admin/customers/{id}/edit", managers(http.HandlerFunc(s.custEdit)))
	mux.Handle("POST /admin/customers/{id}", managers(http.HandlerFunc(s.custSave)))
	mux.Handle("POST /admin/customers/{id}/delete", managers(http.HandlerFunc(s.custDelete)))
	mux.Handle("POST /admin/customers/{id}/recharge", staff(http.HandlerFunc(s.custRecharge)))

	// Old PHP plan.php: voucher list is open to all admins, generate/redeem to staff, delete to managers.
	mux.Handle("GET /admin/vouchers", all(http.HandlerFunc(s.vchList)))
	mux.Handle("GET /admin/vouchers/new", staff(http.HandlerFunc(s.vchNew)))
	mux.Handle("POST /admin/vouchers", staff(http.HandlerFunc(s.vchGenerate)))
	mux.Handle("GET /admin/vouchers/print", all(http.HandlerFunc(s.vchPrint)))
	mux.Handle("GET /admin/vouchers/redeem", staff(http.HandlerFunc(s.vchRedeemForm)))
	mux.Handle("POST /admin/vouchers/redeem", staff(http.HandlerFunc(s.vchRedeem)))
	mux.Handle("POST /admin/vouchers/{id}/delete", managers(http.HandlerFunc(s.vchDelete)))
	mux.Handle("GET /admin/transactions", all(http.HandlerFunc(s.trxList)))

	s.portalRoutes(mux)

	return http.NewCrossOriginProtection().Handler(s.sessions.LoadAndSave(mux))
}

// location is the billing zone; UTC until a billing service is set.
func (s *Server) location() *time.Location {
	if s.Billing != nil && s.Billing.Loc != nil {
		return s.Billing.Loc
	}
	return time.UTC
}

// ts formats a unix time in the billing zone.
func (s *Server) ts(unix int64) string {
	return time.Unix(unix, 0).In(s.location()).Format("2006-01-02 15:04")
}

// money formats rupiah with dot thousands separators: Rp 1.234.000.
func money(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	d := strconv.FormatInt(n, 10)
	for i := len(d) - 3; i > 0; i -= 3 {
		d = d[:i] + "." + d[i:]
	}
	if neg {
		d = "-" + d
	}
	return "Rp " + d
}

// badge maps a status value to its badge colour class.
func badge(v string) string {
	switch strings.ToLower(v) {
	case "active", "enable", "enabled", "unused", "yes":
		return "badge-ok"
	case "disable", "disabled", "banned", "suspended", "expired", "inactive", "no":
		return "badge-bad"
	case "limited":
		return "badge-warn"
	}
	return "badge-muted"
}

// language returns the current app language.
func (s *Server) language() string { return s.lang.Load().(string) }

// loadSettings returns all settings with defaults filled in.
func (s *Server) loadSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.queries.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	m := map[string]string{
		"company_name":  "NuxBill",
		"language":      "indonesia",
		"timezone":      "Asia/Jakarta",
		"currency_code": "Rp",
		"reminder_hour": "7",
	}
	for _, row := range rows {
		m[row.Key] = row.Value
	}
	return m, nil
}

// requireAdmin returns middleware that needs a logged-in, active admin.
// With roles given, the admin must also have one of them.
func (s *Server) requireAdmin(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := s.sessions.GetInt64(r.Context(), "admin_id")
			if id == 0 {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			admin, err := s.queries.GetAdmin(r.Context(), id)
			if err != nil || admin.Status != "Active" {
				if err != nil && err != sql.ErrNoRows {
					slog.Error("load admin", "err", err)
				}
				s.sessions.Destroy(r.Context())
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			if len(roles) > 0 && !contains(roles, admin.Role) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, &admin)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func adminFrom(r *http.Request) *db.Admin {
	a, _ := r.Context().Value(ctxKey{}).(*db.Admin)
	return a
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// clientIP is the host part of RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- templates ----

func (s *Server) parseTemplates() error {
	icons, err := loadIcons()
	if err != nil {
		return err
	}
	funcs := template.FuncMap{
		"T": func(text string) string { return s.catalog.T(s.language(), text) },
		// TT translates the part before ": ", so headings like "Edit Contact: budi" work.
		"TT": func(text string) string {
			if head, rest, ok := strings.Cut(text, ": "); ok {
				return s.catalog.T(s.language(), head) + ": " + rest
			}
			return s.catalog.T(s.language(), text)
		},
		"hasPrefix": strings.HasPrefix,
		"money":     money,
		"badge":     badge,
		"ts":        s.ts,
		"icon": func(name string) (template.HTML, error) {
			svg, ok := icons[name]
			if !ok {
				return "", fmt.Errorf("unknown icon %q", name)
			}
			return svg, nil
		},
	}
	pages := map[string][]string{
		"login":           {"base.html", "login.html"},
		"dashboard":       {"base.html", "app.html", "dashboard.html"},
		"list":            {"base.html", "app.html", "list.html"},
		"form":            {"base.html", "app.html", "form.html"},
		"customer":        {"base.html", "app.html", "customer.html", "radius_usage.html"},
		"radius_sessions": {"base.html", "app.html", "radius_sessions.html"},
		"print":           {"print.html"},

		"p_login":     {"base.html", "portal/login.html"},
		"p_register":  {"base.html", "portal/register.html"},
		"p_dashboard": {"base.html", "portal/layout.html", "portal/dashboard.html"},
		"p_profile":   {"base.html", "portal/layout.html", "portal/profile.html"},
		"p_orders":    {"base.html", "portal/layout.html", "portal/orders.html"},
		"p_plans":     {"base.html", "portal/layout.html", "portal/plans.html"},
	}
	s.templates = map[string]*template.Template{}
	for name, files := range pages {
		for i, f := range files {
			files[i] = path.Join("web/templates", f)
		}
		t, err := template.New("").Funcs(funcs).ParseFS(nuxbill.FS, files...)
		if err != nil {
			return err
		}
		s.templates[name] = t
	}
	return nil
}

// loadIcons reads web/static/icons/*.svg, drops the license comment and
// marks each icon decorative.
func loadIcons() (map[string]template.HTML, error) {
	files, err := fs.Glob(nuxbill.FS, "web/static/icons/*.svg")
	if err != nil {
		return nil, err
	}
	icons := map[string]template.HTML{}
	for _, f := range files {
		b, err := fs.ReadFile(nuxbill.FS, f)
		if err != nil {
			return nil, err
		}
		i := bytes.Index(b, []byte("<svg"))
		if i < 0 {
			return nil, fmt.Errorf("%s: no <svg element", f)
		}
		svg := bytes.Replace(b[i:], []byte("<svg"), []byte(`<svg aria-hidden="true"`), 1)
		icons[strings.TrimSuffix(path.Base(f), ".svg")] = template.HTML(svg)
	}
	return icons, nil
}

// render writes a page. The body is buffered so a template error gives a 500, not half a page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	p.Admin = adminFrom(r)
	p.Path = r.URL.Path
	p.Lang = s.language()
	p.Dir = "ltr"
	if p.Lang == "arabic" {
		p.Dir = "rtl"
	}
	var buf bytes.Buffer
	if err := s.templates[name].ExecuteTemplate(&buf, "base", p); err != nil {
		slog.Error("render", "page", name, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}
