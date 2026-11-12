// Package api serves /api/v1: JSON only, bearer-token auth, read-only for now.
// The session cookie is never read here, so there is no CSRF surface.
// Responses are built from internal/api/dto, never from a db model.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/api/dto"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/service/apitoken"
	"github.com/frand-kod/gobill/internal/settings"
)

// AllRoles are the admin roles, in the order the permission page lists them.
var AllRoles = []string{"SuperAdmin", "Admin", "Agent", "Sales", "Report"}

// Groups are the endpoint groups whose roles can be set. "me" is open to every admin and not listed.
// The defaults match what the API allowed before the roles became a setting.
var Groups = []struct {
	Name    string
	Default []string
}{
	{"customers", []string{"SuperAdmin", "Admin", "Agent", "Sales"}},
	{"network", []string{"SuperAdmin", "Admin"}},
}

// Enabled reports the master switch. It is off until a SuperAdmin turns it on.
func Enabled(st map[string]string) bool { return st["api_enabled"] == "yes" }

// RolesKey is the setting that holds the roles of a group, as a comma list.
func RolesKey(group string) string { return "api_roles_" + group }

// RolesFor returns the roles allowed for a group. SuperAdmin is always in the list.
func RolesFor(st map[string]string, group string) []string {
	var out []string
	if v, ok := st[RolesKey(group)]; ok {
		out = []string{"SuperAdmin"}
		for _, r := range strings.Split(v, ",") {
			if r = strings.TrimSpace(r); r != "SuperAdmin" && contains(AllRoles, r) {
				out = append(out, r)
			}
		}
		return out
	}
	for _, g := range Groups {
		if g.Name == group {
			return g.Default
		}
	}
	return []string{"SuperAdmin"}
}

const (
	defaultPerPage = 25
	maxPerPage     = 100
	rateLimit      = 120 // requests per token per minute
)

type Handlers struct {
	Q      *db.Queries
	Tokens *apitoken.Service
	Store  *settings.Store
	now    func() time.Time

	mu   sync.Mutex
	hits map[int64]*window
}

// window is a fixed one-minute counter.
type window struct {
	start time.Time
	n     int
}

func New(q *db.Queries, tokens *apitoken.Service, st *settings.Store) *Handlers {
	return &Handlers{Q: q, Tokens: tokens, Store: st, now: time.Now, hits: map[int64]*window{}}
}

// Register adds the /api/v1 routes. The roles of each group come from the settings on every request.
func Register(mux *http.ServeMux, h *Handlers) {
	mux.Handle("GET /api/v1/me", h.auth("", h.me))
	mux.Handle("GET /api/v1/customers", h.auth("customers", h.customers))
	mux.Handle("GET /api/v1/customers/{id}", h.auth("customers", h.customer))
	mux.Handle("GET /api/v1/subscriptions", h.auth("customers", h.subscriptions))
	mux.Handle("GET /api/v1/plans", h.auth("network", h.plans))
	mux.Handle("GET /api/v1/routers", h.auth("network", h.routers))
	mux.Handle("GET /api/v1/nas", h.auth("network", h.nas))
	mux.Handle("GET /api/v1/radius/sessions", h.auth("network", h.sessions))
}

type ctxKey struct{}

func adminOf(r *http.Request) db.Admin { return r.Context().Value(ctxKey{}).(db.Admin) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// auth checks the master switch, the bearer token, the rate limit and the role of the group ("" = any admin).
func (h *Handlers) auth(group string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, err := h.Store.View(r.Context())
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !Enabled(st) {
			fail(w, http.StatusForbidden, "API is disabled")
			return
		}
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		t, a, err := h.Tokens.Verify(r.Context(), strings.TrimSpace(tok))
		if err == apitoken.ErrInvalid {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !h.allow(t.ID) {
			w.Header().Set("Retry-After", "60")
			fail(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		if group != "" && !contains(RolesFor(st, group), a.Role) {
			fail(w, http.StatusForbidden, "role not allowed")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
	})
}

// allow counts one request for the token in a fixed one-minute window.
// ponytail: in memory per process and reset on restart; a second process needs a shared counter (M10).
func (h *Handlers) allow(id int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	w := h.hits[id]
	if w == nil || now.Sub(w.start) >= time.Minute {
		if len(h.hits) > 1000 { // drop stale windows so the map cannot grow without bound
			for k, v := range h.hits {
				if now.Sub(v.start) >= time.Minute {
					delete(h.hits, k)
				}
			}
		}
		w = &window{start: now}
		h.hits[id] = w
	}
	w.n++
	return w.n <= rateLimit
}

// paging reads ?page and ?per_page; ok=false means a 400 was already written.
func paging(w http.ResponseWriter, r *http.Request) (page, per int64, ok bool) {
	page, per = 1, defaultPerPage
	for _, p := range []struct {
		key string
		dst *int64
		max int64
	}{{"page", &page, 1 << 31}, {"per_page", &per, maxPerPage}} {
		v := r.URL.Query().Get(p.key)
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > p.max {
			fail(w, http.StatusBadRequest, "bad "+p.key)
			return 0, 0, false
		}
		*p.dst = n
	}
	return page, per, true
}

// list writes one page. The query is asked for per+1 rows; the extra one only tells has_more.
func list[T any](w http.ResponseWriter, page, per int64, rows []T, err error) {
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal error")
		return
	}
	more := int64(len(rows)) > per
	if more {
		rows = rows[:per]
	}
	if rows == nil {
		rows = []T{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows, "page": page, "per_page": per, "has_more": more})
}

func conv[M, D any](in []M, f func(M) D) []D {
	out := make([]D, len(in))
	for i, m := range in {
		out[i] = f(m)
	}
	return out
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": dto.FromAdmin(adminOf(r))})
}

func (h *Handlers) customers(w http.ResponseWriter, r *http.Request) {
	page, per, ok := paging(w, r)
	if !ok {
		return
	}
	var rows []db.Customer
	var err error
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		rows, err = h.Q.SearchCustomers(r.Context(), db.SearchCustomersParams{Q: q, PageOffset: (page - 1) * per, PageLimit: per + 1})
	} else {
		rows, err = h.Q.ListCustomers(r.Context(), db.ListCustomersParams{Limit: per + 1, Offset: (page - 1) * per})
	}
	list(w, page, per, conv(rows, dto.FromCustomer), err)
}

func (h *Handlers) customer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad id")
		return
	}
	c, err := h.Q.GetCustomer(r.Context(), id)
	if err == sql.ErrNoRows {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": dto.FromCustomer(c)})
}

func (h *Handlers) subscriptions(w http.ResponseWriter, r *http.Request) {
	page, per, ok := paging(w, r)
	if !ok {
		return
	}
	cid, err := strconv.ParseInt(r.URL.Query().Get("customer_id"), 10, 64)
	if err != nil || cid < 1 {
		fail(w, http.StatusBadRequest, "customer_id is required")
		return
	}
	rows, err := h.Q.ListSubscriptionsByCustomer(r.Context(), db.ListSubscriptionsByCustomerParams{CustomerID: cid, Limit: per + 1, Offset: (page - 1) * per})
	list(w, page, per, conv(rows, dto.FromSubscription), err)
}

func (h *Handlers) plans(w http.ResponseWriter, r *http.Request) {
	page, per, ok := paging(w, r)
	if !ok {
		return
	}
	rows, err := h.Q.ListPlans(r.Context(), db.ListPlansParams{Limit: per + 1, Offset: (page - 1) * per})
	list(w, page, per, conv(rows, dto.FromPlan), err)
}

func (h *Handlers) routers(w http.ResponseWriter, r *http.Request) {
	page, per, ok := paging(w, r)
	if !ok {
		return
	}
	rows, err := h.Q.ListRouters(r.Context(), db.ListRoutersParams{Limit: per + 1, Offset: (page - 1) * per})
	list(w, page, per, conv(rows, dto.FromRouter), err)
}

// nas has no paged query: the list is short, so one page slices it in memory.
func (h *Handlers) nas(w http.ResponseWriter, r *http.Request) {
	page, per, ok := paging(w, r)
	if !ok {
		return
	}
	rows, err := h.Q.ListNAS(r.Context())
	if start := (page - 1) * per; start >= int64(len(rows)) {
		rows = nil
	} else {
		rows = rows[start:min(int64(len(rows)), start+per+1)]
	}
	list(w, page, per, conv(rows, dto.FromNAS), err)
}

// session is the public shape of an open RADIUS session.
type session struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	NasIP        string `json:"nas_ip"`
	FramedIP     string `json:"framed_ip"`
	MAC          string `json:"mac"`
	StartedAt    int64  `json:"started_at"`
	UpdatedAt    int64  `json:"updated_at"`
	InputOctets  int64  `json:"input_octets"`
	OutputOctets int64  `json:"output_octets"`
}

func (h *Handlers) sessions(w http.ResponseWriter, r *http.Request) {
	page, per, ok := paging(w, r)
	if !ok {
		return
	}
	rows, err := h.Q.SearchOpenRadiusSessions(r.Context(), db.SearchOpenRadiusSessionsParams{
		Q: strings.TrimSpace(r.URL.Query().Get("q")), PageOffset: (page - 1) * per, PageLimit: per + 1})
	list(w, page, per, conv(rows, func(m db.RadiusSession) session {
		return session{m.ID, m.Username, m.NasIp, m.FramedIp, m.Mac, m.StartedAt, m.UpdatedAt, m.InputOctets, m.OutputOctets}
	}), err)
}
