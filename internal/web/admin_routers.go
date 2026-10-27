package web

// Router CRUD and connection test.

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"fmt"
	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/secret"
	"strconv"
)

func routerFields(v, e map[string]string, editing bool) []field {
	pw := text("password", "Password", v, e).as("password").req()
	if editing {
		pw.Required = false
		pw.Hint = "Leave empty to keep the current password"
	}
	pw.Value = "" // never rendered back
	en := text("enabled", "Enabled", v, e).as("checkbox")
	en.Checked = v["enabled"] == "1"
	out := section([]field{
		text("name", "Router Name", v, e).req(),
		text("host", "Host", v, e).req(),
		text("port", "Port", v, e).as("number").req(),
	}, "Connection", "")
	out = append(out, section([]field{text("username", "Username", v, e).req(), pw}, "Login", "")...)
	if editing {
		rp := text("reset_pin", "Reset certificate pin", v, e).as("checkbox").hint("Not pinned yet. The certificate is pinned on the first TLS connection (port 8729). Tick to forget the pin after the router certificate was changed.")
		if v["cert_pin"] != "" {
			rp.Hint = v["cert_pin"] // SHA-256 hex, needs no translation
		}
		out = append(out, section([]field{rp}, "Certificate", "")...)
	}
	return append(out, section([]field{
		text("coordinates", "Coordinates", v, e).as("coords").hint("lat,lng, e.g. -6.2,106.8. Optional; shown on the router map."),
		text("coverage", "Coverage (m)", v, e).as("number"),
		text("description", "Description", v, e), en,
	}, "Other", "")...)
}

func (s *Server) routerList(w http.ResponseWriter, r *http.Request) {
	q, pg := paging(r)
	rows, err := s.queries.SearchRouters(r.Context(), db.SearchRoutersParams{Q: q, PageLimit: pg.limit(), PageOffset: pg.offset()})
	if err != nil {
		s.fail(w, "list routers", err)
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
			seen = s.ts(x.LastSeenAt.Int64)
		}
		lp.Rows = append(lp.Rows, listRow{x.ID, []string{x.Name, fmt.Sprint(x.Host, ":", x.Port), x.Username, on, status, seen}})
	}
	total, err := s.queries.CountRouters(r.Context(), q)
	if err != nil {
		s.fail(w, "count routers", err)
		return
	}
	lp.finish(pg, total)
	s.renderList(w, r, lp)
}

func (s *Server) routerNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"port": "8728", "enabled": "1"}
	s.renderForm(w, r, 200, formPage{"Add Router", "/admin/routers", "/admin/routers", routerFields(v, nil, false)})
}

func (s *Server) routerEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	x, err := s.queries.GetRouter(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get router", err)
		return
	}
	v := map[string]string{"name": x.Name, "host": x.Host, "port": fmt.Sprint(x.Port), "username": x.Username,
		"description": x.Description, "enabled": fmt.Sprint(x.Enabled), "coordinates": x.Coordinates, "coverage": fmt.Sprint(x.Coverage), "cert_pin": x.CertPin}
	s.renderForm(w, r, 200, formPage{"Edit Router", fmt.Sprint("/admin/routers/", id), "/admin/routers", routerFields(v, nil, true)})
}

func (s *Server) routerSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, "name", "host", "port", "username", "description", "enabled", "coordinates", "coverage", "reset_pin")
	pass := r.PostFormValue("password")
	e := map[string]string{}
	for _, k := range []string{"name", "host", "username"} {
		if v[k] == "" {
			e[k] = "This field is required"
		}
	}
	port, ok := posInt(v["port"])
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
		x, err := s.queries.GetRouter(r.Context(), id)
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		} else if err != nil {
			s.fail(w, "get router", err)
			return
		}
		current = x.PasswordEnc
		v["cert_pin"] = x.CertPin
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
				s.fail(w, "seal router password", err)
				return
			}
		}
		enabled := int64(0)
		if v["enabled"] == "1" {
			enabled = 1
		}
		var err error
		if id == 0 {
			_, err = s.queries.CreateRouter(r.Context(), db.CreateRouterParams{Name: v["name"], Host: v["host"], Port: port,
				Username: v["username"], PasswordEnc: enc, Description: v["description"], Enabled: enabled, Coordinates: coords, Coverage: coverage})
		} else {
			err = s.queries.UpdateRouter(r.Context(), db.UpdateRouterParams{Name: v["name"], Host: v["host"], Port: port,
				Username: v["username"], PasswordEnc: enc, Description: v["description"], Enabled: enabled, Coordinates: coords, Coverage: coverage, CertPin: pin, ID: id})
		}
		switch {
		case isUnique(err):
			e["name"] = "Name already exists"
		case err != nil:
			s.fail(w, "save router", err)
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
	x, err := s.queries.GetRouter(r.Context(), pathID(r))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get router", err)
		return
	}
	lang := s.language()
	if s.Billing == nil {
		s.sessions.Put(r.Context(), "error", s.catalog.T(lang, "Router connection is not configured"))
	} else if id, err := s.Billing.Ping(r.Context(), x); err != nil {
		s.putFailure(r, s.catalog.T(lang, "Connection failed")+": "+x.Name+". "+s.catalog.T(lang, routerErr(err, msgRouterOther)), err)
	} else {
		s.sessions.Put(r.Context(), "flash", s.catalog.T(lang, "Connection successful")+": "+x.Name+" ("+id+")")
	}
	http.Redirect(w, r, "/admin/routers", http.StatusSeeOther)
}

// routerSync ("Push all") sends every plan and active subscription of an API-mode router to it,
// then flashes the counts; failures go in the technical details. A RADIUS router is refused and an
// offline router stops the run before any item.
func (s *Server) routerSync(w http.ResponseWriter, r *http.Request) {
	x, err := s.queries.GetRouter(r.Context(), pathID(r))
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get router", err)
		return
	}
	lang := s.language()
	defer http.Redirect(w, r, "/admin/routers", http.StatusSeeOther)
	if s.Billing == nil {
		s.sessions.Put(r.Context(), "error", s.catalog.T(lang, "Router connection is not configured"))
		return
	}
	rep, err := s.Billing.SyncRouter(r.Context(), x.ID, adminFrom(r).ID)
	switch {
	case errors.Is(err, billing.ErrRadiusRouter):
		s.sessions.Put(r.Context(), "error", s.catalog.T(lang, "Router is in RADIUS mode, nothing to push")+": "+x.Name)
	case err != nil:
		s.putFailure(r, s.catalog.T(lang, "Push all failed")+": "+x.Name+". "+s.catalog.T(lang, routerErr(err, msgRouterOther)), err)
	default:
		s.logActivity(r, "router.sync", x.Name)
		sum := fmt.Sprintf("%s: %s, %d %s, %d %s", x.Name, s.catalog.T(lang, "Push all finished"), rep.OK, s.catalog.T(lang, "succeeded"), rep.Failed, s.catalog.T(lang, "failed"))
		if rep.Failed == 0 {
			s.sessions.Put(r.Context(), "flash", sum)
		}
		d := strings.Join(rep.Failures, "; ")
		if len(d) > 2000 {
			d = d[:2000] + "..."
		}
		s.sessions.Put(r.Context(), "error", sum)
		s.sessions.Put(r.Context(), "detail", d)
	}
}

func (s *Server) routerDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if x, err := s.queries.GetRouter(r.Context(), pathID(r)); err == nil {
		name = x.Name
	}
	s.remove(w, r, "/admin/routers", "router", "Router is used by a plan", name, func(id int64) error {
		return s.queries.DeleteRouter(r.Context(), id)
	})
}
