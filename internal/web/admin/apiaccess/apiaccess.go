// Package apiaccess is the SuperAdmin page "API": the master switch, which roles may call each endpoint
// group and the access tokens. All routes are SuperAdmin only.
package apiaccess

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/service/apitoken"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/api"
	"github.com/frand-kod/gobill/internal/web/core"
)

// Handlers serves the API page.
type Handlers struct {
	*adminx.Base
	Tokens *apitoken.Service
}

func New(b *adminx.Base, t *apitoken.Service) *Handlers { return &Handlers{Base: b, Tokens: t} }

// Register adds the routes.
func Register(mux *http.ServeMux, h *Handlers, m adminx.Mw) {
	mux.Handle("GET /admin/api", m.SuperAdmin(http.HandlerFunc(h.page)))
	mux.Handle("POST /admin/api/settings", m.SuperAdmin(http.HandlerFunc(h.saveSettings)))
	mux.Handle("POST /admin/api/tokens", m.SuperAdmin(http.HandlerFunc(h.createToken)))
	mux.Handle("POST /admin/api/tokens/{id}/revoke", m.SuperAdmin(http.HandlerFunc(h.revoke)))
}

type groupRow struct {
	Name  string
	Roles map[string]bool
}

type tokenRow struct {
	ID                                 int64
	Name, Owner, Created, Used, Expiry string
	Status, Badge                      string
	Live                               bool
}

type view struct {
	Enabled bool
	Roles   []string
	Groups  []groupRow
	Admins  []db.Admin
	Tokens  []tokenRow
	Plain   string // the new token, set only on the response of a create
	Name    string // name of the new token
}

func (h *Handlers) build(r *http.Request) (view, error) {
	ctx := r.Context()
	st, err := h.Settings.All(ctx)
	if err != nil {
		return view{}, err
	}
	v := view{Enabled: api.Enabled(st), Roles: api.AllRoles}
	for _, g := range api.Groups {
		row := groupRow{Name: g.Name, Roles: map[string]bool{}}
		for _, role := range api.RolesFor(st, g.Name) {
			row.Roles[role] = true
		}
		v.Groups = append(v.Groups, row)
	}
	rows, err := h.Conn.QueryContext(ctx, `SELECT id, username, fullname, role FROM admins WHERE status = 'Active' ORDER BY username`)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var a db.Admin
		if err := rows.Scan(&a.ID, &a.Username, &a.Fullname, &a.Role); err != nil {
			return v, err
		}
		v.Admins = append(v.Admins, a)
	}
	if err := rows.Err(); err != nil {
		return v, err
	}
	trows, err := h.Conn.QueryContext(ctx, `SELECT t.id, t.name, a.username, a.status, t.created_at, t.last_used_at, t.expires_at, t.revoked_at
		FROM api_tokens t JOIN admins a ON a.id = t.admin_id ORDER BY t.id DESC`)
	if err != nil {
		return v, err
	}
	defer trows.Close()
	now := h.Now().Unix()
	for trows.Next() {
		var t tokenRow
		var ostatus string
		var created, exp int64
		var used, rev *int64
		if err := trows.Scan(&t.ID, &t.Name, &t.Owner, &ostatus, &created, &used, &exp, &rev); err != nil {
			return v, err
		}
		t.Created, t.Used, t.Expiry = h.TS(created), "-", "-"
		if used != nil {
			t.Used = h.TS(*used)
		}
		if exp > 0 {
			t.Expiry = h.TS(exp)
		}
		switch {
		case rev != nil:
			t.Status, t.Badge = "Revoked", "badge-bad"
		case exp > 0 && exp <= now:
			t.Status, t.Badge = "Expired", "badge-warn"
		case ostatus != "Active":
			t.Status, t.Badge = "Owner inactive", "badge-warn"
		default:
			t.Status, t.Badge, t.Live = "Active", "badge-ok", true
		}
		v.Tokens = append(v.Tokens, t)
	}
	return v, trows.Err()
}

// render shows the page. It is never cached, because the create response carries the token.
func (h *Handlers) render(w http.ResponseWriter, r *http.Request, status int, v view, errMsg string) {
	w.Header().Set("Cache-Control", "no-store")
	h.Render(w, r, status, "apiaccess", core.Page{Title: "API", Error: errMsg, Data: v})
}

func (h *Handlers) page(w http.ResponseWriter, r *http.Request) {
	v, err := h.build(r)
	if err != nil {
		h.Fail(w, "api page", err)
		return
	}
	h.render(w, r, http.StatusOK, v, "")
}

// again re-renders the page with an error message.
func (h *Handlers) again(w http.ResponseWriter, r *http.Request, code int, msg string) {
	v, err := h.build(r)
	if err != nil {
		h.Fail(w, "api page", err)
		return
	}
	h.render(w, r, code, v, h.T(msg))
}

func (h *Handlers) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	vals := map[string]string{"api_enabled": "no"}
	if r.PostForm.Get("api_enabled") == "yes" {
		vals["api_enabled"] = "yes"
	}
	for _, g := range api.Groups {
		keep := []string{"SuperAdmin"}
		for _, role := range r.PostForm["roles_"+g.Name] {
			if !slices.Contains(api.AllRoles, role) {
				h.again(w, r, http.StatusBadRequest, "Unknown role")
				return
			}
			if !slices.Contains(keep, role) {
				keep = append(keep, role)
			}
		}
		vals[api.RolesKey(g.Name)] = strings.Join(keep, ",")
	}
	for k, val := range vals {
		if err := h.Queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: k, Value: val}); err != nil {
			h.Fail(w, "save api settings", err)
			return
		}
	}
	h.Settings.Invalidate()
	h.LogActivity(r, "api.settings", "enabled="+vals["api_enabled"]+" customers="+vals["api_roles_customers"]+" network="+vals["api_roles_network"])
	h.Sessions.Put(r.Context(), "flash", h.T("Saved"))
	http.Redirect(w, r, "/admin/api", http.StatusSeeOther)
}

var ttls = map[string]time.Duration{"30": 30 * 24 * time.Hour, "90": 90 * 24 * time.Hour, "365": 365 * 24 * time.Hour, "0": 0}

func (h *Handlers) createToken(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	id, _ := strconv.ParseInt(r.PostFormValue("admin_id"), 10, 64)
	ttl, okTTL := ttls[r.PostFormValue("ttl")]
	if name == "" || len(name) > 100 || !okTTL {
		h.again(w, r, http.StatusBadRequest, "Enter a name and pick how long the token lasts")
		return
	}
	owner, err := h.Queries.GetAdmin(r.Context(), id)
	if err != nil || owner.Status != "Active" {
		h.again(w, r, http.StatusBadRequest, "Pick an active admin")
		return
	}
	plain, t, err := h.Tokens.Create(r.Context(), owner.ID, name, ttl)
	if err != nil {
		h.Fail(w, "create api token", err)
		return
	}
	h.LogActivity(r, "api.token.create", "#"+strconv.FormatInt(t.ID, 10)+" "+name+" for "+owner.Username)
	v, err := h.build(r)
	if err != nil {
		h.Fail(w, "api page", err)
		return
	}
	v.Plain, v.Name = plain, name
	h.render(w, r, http.StatusOK, v, "")
}

func (h *Handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := h.Tokens.Revoke(r.Context(), id); err != nil {
		h.Sessions.Put(r.Context(), "error", h.T("Token not found or already revoked"))
	} else {
		h.LogActivity(r, "api.token.revoke", "#"+strconv.FormatInt(id, 10))
		h.Sessions.Put(r.Context(), "flash", h.T("Token revoked"))
	}
	http.Redirect(w, r, "/admin/api", http.StatusSeeOther)
}
