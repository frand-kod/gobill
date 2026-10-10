// Package web holds the HTTP server: routes, middleware, handlers and templates.
package web

// Server type, construction, shared accessors and small helpers.

import (
	"database/sql"
	"html/template"
	"net/http"
	"sync/atomic"

	"context"
	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
	nuxbill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/i18n"
	"github.com/frand-kod/gobill/internal/radius"
	"golang.org/x/crypto/bcrypt"
	"net"
	"strings"
	"sync"
	"time"
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
	guides    *guideSet    // rendered "Panduan" pages, see docs.go
	lang      atomic.Value // string: current language, a global app setting
	dummyHash []byte       // compared against when the username does not exist

	// Radius is shared with the UDP listener so auth throttles apply to /radius.php too.
	Radius     *radius.Server
	radiusOnce sync.Once

	// Version is shown in the admin sidebar footer.
	Version string

	// ClockWarning, if set, returns a non-empty reason while the clock is untrusted.
	ClockWarning func() string
	// SecretKey encrypts router passwords and customer secrets (see package secret).
	SecretKey       []byte
	SettingsChanged func(ctx context.Context) // called after settings are saved
	// Billing recharges customers and syncs plans to routers; nil disables both.
	Billing *billing.Service
	// CoAPort is the NAS Disconnect-Request port; empty = 3799.
	CoAPort string
	// NewGateway builds the online gateway from tripay_* style config; nil = Tripay. Tests replace it.
	NewGateway func(cfg map[string]string) (Gateway, error)
	chanCache  paymentCache

	// ponytail: in-memory limiter, resets on restart; persist if needed
	mu      sync.Mutex
	otpSent map[string][]time.Time // "ip:x" / "ph:y" -> OTP send times, see otpAllow
	failed  map[string][]time.Time // client IP -> times of recent failed logins

	idle   atomic.Int64 // admin idle timeout in ns, see ReloadSessionSettings
	single atomic.Bool  // single_session
	trust  atomic.Bool  // trust_proxy, see realIP
}

// Page is the data every template receives.
type Page struct {
	Title         string
	Admin         *db.Admin
	Customer      *db.Customer // portal customer, if any
	Impersonating bool         // portal session opened by an admin (login as customer)
	Unread        int64        // portal inbox badge
	Flash         string
	Error         string
	Warn          string // yellow alert: the action worked but needs a follow-up
	Detail        string // raw technical text of Error, shown in a small <details>
	ErrLink       string // href of an "Add balance" button next to Error
	Intro         intro  // callout under the page title; filled by render when empty
	Path          string
	Tabs          []option          // settings sub-page menu
	Brand         map[string]string // logo and login page text, filled by render
	Dir           string            // "rtl" or "ltr"
	Lang          string
	Version       string
	Data          any
}

type ctxKey struct{}

func New(conn *sql.DB, secureCookie bool) (*Server, error) {
	store := sqlite3store.New(conn)
	sm := scs.New()
	sm.Store = store
	sm.Lifetime = 12 * time.Hour
	// idle timeout is enforced by idleGuard so it can change at runtime
	sm.Cookie.Name = "nuxbill_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = secureCookie

	catalog, err := i18n.Load(nuxbill.FS, "lang")
	if err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcryptCost)
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
	s.ReloadSessionSettings(context.Background())
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	if s.guides, err = loadGuides(nuxbill.Docs); err != nil {
		return nil, err
	}
	return s, nil
}

// location is the billing zone; UTC until a billing service is set.
func (s *Server) location() *time.Location {
	if s.Billing != nil {
		return s.Billing.Location()
	}
	return time.UTC
}

// ts formats a unix time in the billing zone.
func (s *Server) ts(unix int64) string {
	return time.Unix(unix, 0).In(s.location()).Format("2006-01-02 15:04")
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
		"company_name":  "gobill",
		"language":      "indonesia",
		"timezone":      "Asia/Jakarta",
		"currency_code": "Rp",
		"reminder_hour": "7",
	}
	for _, row := range rows {
		m[row.Key] = row.Value
	}
	setThousandsSep(m["thousands_sep"]) // money reads it; loaded at start and on each settings page
	return m, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// realIP replaces RemoteAddr with the rightmost X-Forwarded-For entry (added by our own proxy) when trust_proxy=yes.
func (s *Server) realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" && s.trust.Load() {
			r.RemoteAddr = strings.TrimSpace(xf[strings.LastIndexByte(xf, ',')+1:])
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the host part of RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
