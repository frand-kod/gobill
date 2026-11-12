// Package core holds what the web handler packages share: sessions, translations, settings, page
// rendering, the login and OTP limiters and the payment gateway helpers. It imports no handler package.
package core

import (
	"context"
	"html/template"
	"maps"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/i18n"
	"github.com/frand-kod/gobill/internal/service/customer"
	"github.com/frand-kod/gobill/internal/settings"
	"github.com/frand-kod/gobill/internal/web/view"
)

// Core is the state and helpers every handler package uses. The web Server embeds it.
type Core struct {
	Queries   *db.Queries
	Sessions  *scs.SessionManager
	Catalog   i18n.Catalog
	Lang      atomic.Value // string: current language, a global app setting
	Settings  *settings.Store
	Templates map[string]*template.Template
	DummyHash []byte // compared against when the username does not exist

	// Version is shown in the admin sidebar footer and on /health.
	Version string
	// SecretKey encrypts router passwords and customer secrets (see package secret).
	SecretKey []byte
	// Billing recharges customers and syncs plans to routers; nil disables both.
	Billing *billing.Service
	// Customers holds the write logic of the customer forms; read it through CustomerSvc().
	Customers *customer.Service
	// NewGateway builds the online gateway from tripay_* style config; nil = Tripay. Tests replace it.
	NewGateway func(cfg map[string]string) (Gateway, error)
	ChanCache  paymentCache

	// Mu guards the in-memory limiter maps (and the web Server's own small maps).
	// ponytail: in-memory limiter, resets on restart; persist if needed. Login locks per ip|username; per IP, 100 failures/15 min across usernames
	Mu       sync.Mutex
	OtpSent  map[string][]time.Time // "ip:x" / "ph:y" -> OTP send times, see OtpAllow
	Failed   map[string][]time.Time // client IP -> times of recent failed logins
	totpLast map[int64]int64        // admin id -> last accepted TOTP time step, see TotpVerify

	// Live is the rate samples of the open RADIUS sessions, see LiveMonitor.
	Live LiveMonitor

	// Asset adds the cache-busting token to a static path; web sets it (it owns the embedded files).
	Asset func(p string) string
	// Decorate fills the admin-only parts of a Page (intro, update notice, setup state, bell); web sets it.
	Decorate func(r *http.Request, name string, p *Page)
}

// AdminKey is the request context key of the signed-in admin.
type AdminKey struct{}

// AdminFrom is the admin the auth middleware put on the request, or nil.
func AdminFrom(r *http.Request) *db.Admin {
	a, _ := r.Context().Value(AdminKey{}).(*db.Admin)
	return a
}

// Language returns the current app language.
func (s *Core) Language() string { return s.Lang.Load().(string) }

// T translates key into the app language.
func (s *Core) T(key string) string { return s.Catalog.T(s.Language(), key) }

// CustomerSvc returns the customer service with the current secret key, which app sets after New.
func (s *Core) CustomerSvc() *customer.Service {
	c := *s.Customers
	c.Key = s.SecretKey
	return &c
}

// Location is the billing zone; UTC until a billing service is set.
func (s *Core) Location() *time.Location {
	if s.Billing != nil {
		return s.Billing.Location()
	}
	return time.UTC
}

// TS formats a unix time in the billing zone.
func (s *Core) TS(unix int64) string {
	return time.Unix(unix, 0).In(s.Location()).Format("2006-01-02 15:04")
}

// SubExpiry is a subscription's expiry as shown: a start_on_first_login subscription has none until its first login.
func (s *Core) SubExpiry(pending, unix int64) string {
	if pending == 1 {
		return s.T("Starts on first login")
	}
	return s.TS(unix)
}

// LoadSettings returns all settings with defaults filled in.
func (s *Core) LoadSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.Settings.All(ctx)
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
	maps.Copy(m, rows)
	view.SetThousandsSep(m["thousands_sep"]) // money reads it; loaded at start and on each settings page
	return m, nil
}

// FlashTo stores a translated flash message and redirects.
func (s *Core) FlashTo(w http.ResponseWriter, r *http.Request, to, msg string) {
	s.Sessions.Put(r.Context(), "flash", s.T(msg))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ClientIP is the host part of RemoteAddr.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// SetupKey is the settings key of the setup wizard state: "" (not started), "started", "dismissed" or "done".
const SetupKey = "setup_wizard"
