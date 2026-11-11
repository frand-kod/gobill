package web

// Router CRUD and connection test.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"fmt"
	"strconv"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"github.com/frand-kod/gobill/internal/wg"
)

func routerFields(v, e map[string]string, editing bool) []field {
	pw := text("password", "Password", v, e).as("password").req()
	if editing {
		pw.Required = false
		pw.Hint = "Leave empty to keep the current password (re-enter it if you change host or port)"
	}
	pw.Value = "" // never rendered back
	host, port := text("host", "Host", v, e).req(), text("port", "Port", v, e).as("number").req()
	if editing { // the stored password only fits the stored host and port
		host, port = host.bind(), port.bind()
		pw = pw.reqIf(fmt.Sprintf("host !== %s || port !== %s", jsonStr(v["orig_host"]), jsonStr(v["orig_port"])))
	} else {
		port = port.bind()
	}
	en := text("enabled", "Enabled", v, e).as("checkbox")
	en.Checked = v["enabled"] == "1"
	tlsField := text("tls", "API connection", v, e).as("select")
	tlsField.Options = []option{{"0", "Plain API (usually port 8728)"}, {"1", "API-SSL, TLS (usually port 8729)"}}
	tlsField.Hint = "Plain API sends the password unencrypted: use it only on a LAN or VPN, and API-SSL across the internet."
	tlsField.Bind = true
	tlsField.Change = "if ($event.target.value === '1' && port === '8728') port = '8729'; else if ($event.target.value === '0' && port === '8729') port = '8728'"
	out := section([]field{
		text("name", "Router Name", v, e).req(),
		host,
		port,
		tlsField,
	}, "Connection", "")
	out = append(out, section([]field{text("username", "Username", v, e).req(), pw}, "Login", "")...)
	if editing {
		rp := text("reset_pin", "Reset certificate pin", v, e).as("checkbox").hint("Not pinned yet. The certificate is pinned on the first TLS connection (API-SSL). Tick to forget the pin after the router certificate was changed.")
		if v["cert_pin"] != "" {
			rp.Hint = v["cert_pin"] // SHA-256 hex, needs no translation
		}
		out = append(out, section([]field{rp}, "Certificate", "")...)
		out = append(out, field{Name: "reconcile", Label: "Router accounts", Type: "link", Value: fmt.Sprint("/admin/routers/", v["id"], "/reconcile"), Btn: "Reconcile with router", Section: "Accounts",
			Hint: "Read the PPPoE and hotspot accounts on the router and compare them with gobill. Nothing changes until you confirm."})
	}
	return append(out, section([]field{
		text("coordinates", "Coordinates", v, e).as("coords").hint("lat,lng, e.g. -6.2,106.8. Optional; shown on the router map."),
		text("coverage", "Coverage (m)", v, e).as("number"),
		text("description", "Description", v, e), en,
	}, "Other", "")...)
}

func (s *Server) routerList(w http.ResponseWriter, r *http.Request) {
	q, pg := view.Paging(r)
	rows, err := s.Queries.SearchRouters(r.Context(), db.SearchRoutersParams{Q: q, PageLimit: pg.Limit(), PageOffset: pg.Offset()})
	if err != nil {
		s.Fail(w, "list routers", err)
		return
	}
	lp := listPage{Heading: "Routers", Base: "/admin/routers", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Cols:    []string{"Name", "Host", "Username", "Enabled", "Status", "Last Seen"},
		Actions: []rowAction{{"test", "Test connection", "", false}, {"sync", "Push all", "", true}}}
	for _, x := range rows {
		on := "No"
		if x.Enabled == 1 {
			on = "Yes"
		}
		status, seen := "Unknown", ""
		if x.Online.Valid {
			status = map[bool]string{true: "Online", false: "Offline"}[x.Online.Int64 == 1]
		}
		if x.LastSeenAt.Valid {
			seen = s.TS(x.LastSeenAt.Int64)
		}
		lp.Rows = append(lp.Rows, listRow{x.ID, []string{x.Name, fmt.Sprint(x.Host, ":", x.Port), x.Username, on, status, seen}})
	}
	// a toolbar link to the latest "Push all" run of each router on this page; only the keys are copied under the lock
	syncs.mu.Lock()
	ran := make(map[int64]bool, len(syncs.runs))
	for id := range syncs.runs {
		ran[id] = true
	}
	syncs.mu.Unlock()
	for _, x := range rows {
		if ran[x.ID] {
			lp.Links = append(lp.Links, option{fmt.Sprintf("/admin/routers/%d/sync", x.ID), s.T("Last push") + ": " + x.Name})
		}
	}
	slices.SortFunc(lp.Links, func(a, b option) int { return strings.Compare(a.Label, b.Label) })
	total, err := s.Queries.CountRouters(r.Context(), q)
	if err != nil {
		s.Fail(w, "count routers", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) routerNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"port": "8728", "tls": "0", "enabled": "1"}
	s.renderForm(w, r, 200, formPage{"Add Router", "/admin/routers", "/admin/routers", routerFields(v, nil, false)})
}

func (s *Server) routerEdit(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	x, err := s.Queries.GetRouter(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get router", err)
		return
	}
	v := map[string]string{"name": x.Name, "host": x.Host, "port": fmt.Sprint(x.Port), "tls": fmt.Sprint(x.Tls), "username": x.Username,
		"id": fmt.Sprint(x.ID), "description": x.Description, "enabled": fmt.Sprint(x.Enabled), "coordinates": x.Coordinates, "coverage": fmt.Sprint(x.Coverage), "cert_pin": x.CertPin,
		"orig_host": x.Host, "orig_port": fmt.Sprint(x.Port)}
	fields := routerFields(v, nil, true)
	if adminFrom(r).Role == "SuperAdmin" {
		fields = append(fields, field{Name: "migration", Label: "Move to RADIUS", Type: "link", Value: fmt.Sprint("/admin/routers/", id, "/migration"), Btn: "Migrate to RADIUS", Section: "RADIUS migration",
			Hint: "Test a RADIUS login, turn on use-radius, then disable the local accounts. Can be rolled back."})
	}
	if wg.Built && s.WG != nil && adminFrom(r).Role == "SuperAdmin" {
		st, _ := s.LoadSettings(r.Context())
		n, _ := s.Queries.CountWgPeers(r.Context())
		if once := s.Sessions.PopString(r.Context(), "wg_once"); st["wg_enabled"] == "yes" || n > 0 || once != "" {
			fields = append(fields, s.routerWGFields(r.Context(), x, once, wgDefaults(st)["wg_port"])...)
		}
	}
	s.renderForm(w, r, 200, formPage{"Edit Router", fmt.Sprint("/admin/routers/", id), "/admin/routers", fields})
}

func (s *Server) routerSave(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	v := httpx.FormVals(r, "name", "host", "port", "tls", "username", "description", "enabled", "coordinates", "coverage", "reset_pin")
	pass := r.PostFormValue("password")
	e := map[string]string{}
	for _, k := range []string{"name", "host", "username"} {
		if v[k] == "" {
			e[k] = "This field is required"
		}
	}
	port, ok := httpx.PosInt(v["port"])
	if !ok || port > 65535 {
		e["port"] = "Enter a port between 1 and 65535"
	}
	coords, ok := checkCoords(v["coordinates"])
	if !ok {
		e["coordinates"] = "Enter coordinates as lat,lng, e.g. -6.2,106.8"
	}
	coverage, err := strconv.ParseInt(orZero(v["coverage"]), 10, 64)
	if err != nil || coverage < 0 {
		e["coverage"] = "Enter meters, 0 or more"
	}
	var current []byte
	var pin string
	if id != 0 {
		x, err := s.Queries.GetRouter(r.Context(), id)
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.Fail(w, "get router", err)
			return
		}
		current = x.PasswordEnc
		if pass == "" && (v["host"] != x.Host || port != x.Port) {
			e["password"] = "Re-enter the password when the host or port changes"
		}
		v["cert_pin"] = x.CertPin
		v["orig_host"], v["orig_port"] = x.Host, fmt.Sprint(x.Port)
		if v["reset_pin"] != "1" {
			pin = x.CertPin
		}
	}
	if pass == "" && id == 0 {
		e["password"] = "This field is required"
	}
	if len(e) == 0 {
		enc := current
		if pass != "" {
			var err error
			if enc, err = secret.Seal(s.SecretKey, []byte(pass)); err != nil {
				s.Fail(w, "seal router password", err)
				return
			}
		}
		enabled := int64(0)
		if v["enabled"] == "1" {
			enabled = 1
		}
		tlsMode := int64(0)
		if v["tls"] == "1" {
			tlsMode = 1
		}
		var err error
		if id == 0 {
			var rt db.Router
			if rt, err = s.Queries.CreateRouter(r.Context(), db.CreateRouterParams{Name: v["name"], Host: v["host"], Port: port,
				Username: v["username"], PasswordEnc: enc, Description: v["description"], Enabled: enabled, Coordinates: coords, Coverage: coverage}); err == nil {
				err = s.Queries.SetRouterTLS(r.Context(), db.SetRouterTLSParams{Tls: tlsMode, ID: rt.ID})
			}
		} else {
			if err = s.Queries.UpdateRouter(r.Context(), db.UpdateRouterParams{Name: v["name"], Host: v["host"], Port: port,
				Username: v["username"], PasswordEnc: enc, Description: v["description"], Enabled: enabled, Coordinates: coords, Coverage: coverage, CertPin: pin, ID: id}); err == nil {
				err = s.Queries.SetRouterTLS(r.Context(), db.SetRouterTLSParams{Tls: tlsMode, ID: id})
			}
		}
		switch {
		case httpx.IsUnique(err):
			e["name"] = "Name already exists"
		case err != nil:
			s.Fail(w, "save router", err)
			return
		default:
			act, msg := "router.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "router.update", "Data Updated Successfully"
			}
			s.done(w, r, "/admin/routers", msg, act, v["name"])
			return
		}
	}
	action, head := "/admin/routers", "Add Router"
	if id != 0 {
		action, head = fmt.Sprint("/admin/routers/", id), "Edit Router"
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/routers", routerFields(v, e, id != 0)})
}

// routerTest connects to the router and flashes the identity or the error.
func (s *Server) routerTest(w http.ResponseWriter, r *http.Request) {
	x, err := s.Queries.GetRouter(r.Context(), httpx.PathID(r))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get router", err)
		return
	}
	lang := s.Language()
	if s.Billing == nil {
		s.Sessions.Put(r.Context(), "error", s.Catalog.T(lang, "Router connection is not configured"))
	} else if id, err := s.Billing.Ping(r.Context(), x); err != nil {
		s.putFailure(r, s.Catalog.T(lang, "Connection failed")+": "+x.Name+". "+s.Catalog.T(lang, routerErr(err, msgRouterOther)), err)
	} else {
		s.Sessions.Put(r.Context(), "flash", s.Catalog.T(lang, "Connection successful")+": "+x.Name+" ("+id+")")
	}
	http.Redirect(w, r, "/admin/routers", http.StatusSeeOther)
}

// syncJobs holds the "Push all" run of each router, one at a time per router, like bulk for messages.
// ponytail: in-memory, a restart forgets the runs; store them in a table if they must survive.
type syncJobs struct {
	mu   sync.Mutex
	runs map[int64]*syncRun
}

type syncRun struct {
	active                bool
	total, done, ok, fail int
	stopped               string // why the run ended early, e.g. "router offline: ..."
	failures              []string
}

var syncs = syncJobs{runs: map[int64]*syncRun{}}

func (j *syncJobs) start(id int64) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if r := j.runs[id]; r != nil && r.active {
		return false
	}
	j.runs[id] = &syncRun{active: true}
	return true
}

// update changes the run of router id under the lock.
func (j *syncJobs) update(id int64, f func(*syncRun)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f(j.runs[id])
}

// runSync pushes in its own goroutine with a background context: closing the page must not cancel it.
func (s *Server) runSync(id, adminID int64) {
	err := s.Billing.SyncRouter(context.Background(), id, adminID,
		func(n int) { syncs.update(id, func(r *syncRun) { r.total = n }) },
		func(what string, err error) {
			syncs.update(id, func(r *syncRun) {
				r.done++
				if err != nil {
					r.fail++
					r.failures = append(r.failures, what+": "+err.Error())
				} else {
					r.ok++
				}
			})
		})
	syncs.update(id, func(r *syncRun) {
		r.active = false
		if err != nil {
			r.stopped = err.Error()
		}
	})
}

// routerSync ("Push all") starts the background push of every plan and active subscription of an
// API-mode router, then shows its status page. A RADIUS router is refused before a run exists, a
// second click during a run is refused; the offline check happens inside the run.
func (s *Server) routerSync(w http.ResponseWriter, r *http.Request) {
	x, err := s.Queries.GetRouter(r.Context(), httpx.PathID(r))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get router", err)
		return
	}
	lang := s.Language()
	if s.Billing == nil {
		s.Sessions.Put(r.Context(), "error", s.Catalog.T(lang, "Router connection is not configured"))
		http.Redirect(w, r, "/admin/routers", http.StatusSeeOther)
		return
	}
	if err := s.Billing.CheckRouterPush(r.Context(), x.ID); errors.Is(err, billing.ErrRadiusRouter) {
		s.Sessions.Put(r.Context(), "error", s.Catalog.T(lang, "Router is in RADIUS mode, nothing to push")+": "+x.Name)
		http.Redirect(w, r, "/admin/routers", http.StatusSeeOther)
		return
	} else if err != nil {
		s.Fail(w, "push check", err)
		return
	}
	if !syncs.start(x.ID) {
		s.Sessions.Put(r.Context(), "error", s.Catalog.T(lang, "A push is already running for this router")+": "+x.Name)
	} else {
		go s.runSync(x.ID, adminFrom(r).ID)
		s.logActivity(r, "router.sync", x.Name)
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/routers/%d/sync", x.ID), http.StatusSeeOther)
}

// routerSyncStatus shows the latest "Push all" run of a router and reloads itself while it runs.
func (s *Server) routerSyncStatus(w http.ResponseWriter, r *http.Request) {
	x, err := s.Queries.GetRouter(r.Context(), httpx.PathID(r))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get router", err)
		return
	}
	base := fmt.Sprintf("/admin/routers/%d/sync", x.ID)
	lp := listPage{Heading: "Push all", Note: x.Name, Base: base, Cols: []string{"Item", "Result"},
		Links: []option{{base, "Refresh"}, {"/admin/routers", "Routers"}}}
	row := func(k, v string) { lp.Rows = append(lp.Rows, listRow{0, []string{s.T(k), v}}) }
	syncs.mu.Lock()
	run := syncs.runs[x.ID]
	if run == nil {
		row("Progress", s.T("No push has run yet"))
	} else {
		state := "Finished"
		switch {
		case run.active:
			state = "Running"
			w.Header().Set("Refresh", "3")
		case run.stopped != "":
			state = "Stopped"
		}
		row("Push state", s.T(state))
		row("Progress", fmt.Sprintf("%d / %d", run.done, run.total))
		row("Succeeded", strconv.Itoa(run.ok))
		row("Failed", strconv.Itoa(run.fail))
		if run.stopped != "" {
			row("Stopped", run.stopped)
		}
		for _, f := range run.failures {
			row("Failed", f)
		}
	}
	syncs.mu.Unlock()
	s.renderList(w, r, lp)
}

func (s *Server) routerDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if x, err := s.Queries.GetRouter(r.Context(), httpx.PathID(r)); err == nil {
		name = x.Name
	}
	s.remove(w, r, "/admin/routers", "router", "Router is used by a plan", name, func(id int64) error {
		peer, perr := s.Queries.GetWgPeer(r.Context(), id)
		if err := s.Queries.DeleteRouter(r.Context(), id); err != nil {
			return err
		}
		if perr == nil { // the peer row went with the router; the tunnel and its NAS row go too
			return s.wgRelease(r.Context(), peer)
		}
		return nil
	})
}
