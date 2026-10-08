package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/frand-kod/nuxbill-go/internal/db"
	"github.com/frand-kod/nuxbill-go/internal/secret"
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
		text("host", "Host", v, e).req().hint("IP address or hostname"),
		text("port", "Port", v, e).as("number").req(),
	}, "Connection", "")
	out = append(out, section([]field{text("username", "Username", v, e).req(), pw}, "Login", "")...)
	return append(out, section([]field{
		text("coordinates", "Coordinates", v, e).as("coords").hint("lat,lng, e.g. -6.2,106.8. Optional; shown on the router map."),
		text("coverage", "Coverage (m)", v, e).as("number").hint("Radius of the wireless coverage in meters. Optional."),
		text("description", "Description", v, e), en,
	}, "Other", "")...)
}

func (s *Server) routerList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchRouters(r.Context(), db.SearchRoutersParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list routers", err)
		return
	}
	lp := listPage{Heading: "Routers", Base: "/admin/routers", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Cols:    []string{"Name", "Host", "Username", "Enabled"},
		Actions: []rowAction{{"test", "Test connection", ""}}}
	for _, x := range rows {
		on := "No"
		if x.Enabled == 1 {
			on = "Yes"
		}
		lp.Rows = append(lp.Rows, listRow{x.ID, []string{x.Name, fmt.Sprint(x.Host, ":", x.Port), x.Username, on}})
	}
	lp.finish(page)
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
		"description": x.Description, "enabled": fmt.Sprint(x.Enabled), "coordinates": x.Coordinates, "coverage": fmt.Sprint(x.Coverage)}
	s.renderForm(w, r, 200, formPage{"Edit Router", fmt.Sprint("/admin/routers/", id), "/admin/routers", routerFields(v, nil, true)})
}

func (s *Server) routerSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, "name", "host", "port", "username", "description", "enabled", "coordinates", "coverage")
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
				Username: v["username"], PasswordEnc: enc, Description: v["description"], Enabled: enabled, Coordinates: coords, Coverage: coverage, ID: id})
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
		s.sessions.Put(r.Context(), "error", s.catalog.T(lang, "Connection failed")+": "+x.Name+": "+err.Error())
	} else {
		s.sessions.Put(r.Context(), "flash", s.catalog.T(lang, "Connection successful")+": "+x.Name+" ("+id+")")
	}
	http.Redirect(w, r, "/admin/routers", http.StatusSeeOther)
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
