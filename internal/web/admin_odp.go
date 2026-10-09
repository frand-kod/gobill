package web

// ODP (distribution point) CRUD.

import (
	"database/sql"
	"net/http"

	"fmt"
	"github.com/frand-kod/gobill/internal/db"
	"strconv"
)

var odpNames = []string{"name", "coordinates", "address", "port_amount", "attenuation", "coverage", "description", "router_id"}

// odpFields builds the ODP form; routers fills the optional router select.
func odpFields(v, e map[string]string, routers []db.Router) []field {
	ro := []option{{"", "-"}}
	for _, x := range routers {
		ro = append(ro, option{fmt.Sprint(x.ID), x.Name})
	}
	rt := text("router_id", "Router", v, e)
	rt.Type, rt.Options = "select", ro
	out := section([]field{
		text("name", "ODP Name", v, e).req(),
		text("coordinates", "Coordinates", v, e).as("coords").hint("lat,lng, e.g. -6.2,106.8. Optional; shown on the ODP map."),
		rt,
	}, "Basic", "")
	return append(out, section([]field{
		text("port_amount", "Port Amount", v, e).as("number"),
		text("attenuation", "Attenuation", v, e),
		text("coverage", "Coverage (m)", v, e).as("number"),
		text("address", "Address", v, e),
		text("description", "Description", v, e),
	}, "Specification", "")...)
}

func (s *Server) odpList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchODPs(r.Context(), db.SearchODPsParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list odps", err)
		return
	}
	lp := listPage{Heading: "ODP", Base: "/admin/odp", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Cols: []string{"Name", "Port Amount", "Attenuation", "Address", "Coverage", "Coordinates"}}
	for _, o := range rows {
		lp.Rows = append(lp.Rows, listRow{o.ID, []string{o.Name, fmt.Sprint(o.PortAmount), o.Attenuation, o.Address,
			fmt.Sprint(o.Coverage), o.Coordinates}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) odpRouters(w http.ResponseWriter, r *http.Request) ([]db.Router, bool) {
	rs, err := s.queries.ListRouters(r.Context(), db.ListRoutersParams{Limit: 500})
	if err != nil {
		s.fail(w, "list routers", err)
		return nil, false
	}
	return rs, true
}

func (s *Server) odpNew(w http.ResponseWriter, r *http.Request) {
	rs, ok := s.odpRouters(w, r)
	if !ok {
		return
	}
	s.renderForm(w, r, 200, formPage{"Add ODP", "/admin/odp", "/admin/odp", odpFields(map[string]string{}, nil, rs)})
}

func (s *Server) odpEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	o, err := s.queries.GetODP(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get odp", err)
		return
	}
	rs, ok := s.odpRouters(w, r)
	if !ok {
		return
	}
	v := map[string]string{"name": o.Name, "coordinates": o.Coordinates, "address": o.Address, "port_amount": fmt.Sprint(o.PortAmount),
		"attenuation": o.Attenuation, "coverage": fmt.Sprint(o.Coverage), "description": o.Description}
	if o.RouterID.Valid {
		v["router_id"] = fmt.Sprint(o.RouterID.Int64)
	}
	s.renderForm(w, r, 200, formPage{"Edit ODP", fmt.Sprint("/admin/odp/", id), "/admin/odp", odpFields(v, nil, rs)})
}

// odpSave serves both create (no {id} in the path) and update.
func (s *Server) odpSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, odpNames...)
	e := map[string]string{}
	if v["name"] == "" {
		e["name"] = "This field is required"
	}
	coords, ok := checkCoords(v["coordinates"])
	if !ok {
		e["coordinates"] = "Enter coordinates as lat,lng, e.g. -6.2,106.8"
	}
	ports, err := strconv.ParseInt(orZero(v["port_amount"]), 10, 64)
	if err != nil || ports < 0 {
		e["port_amount"] = "Enter a number, 0 or more"
	}
	cover, err := strconv.ParseInt(orZero(v["coverage"]), 10, 64)
	if err != nil || cover < 0 {
		e["coverage"] = "Enter meters, 0 or more"
	}
	router := sql.NullInt64{}
	if v["router_id"] != "" {
		rid, ok := posInt(v["router_id"])
		if !ok {
			e["router_id"] = "Invalid value"
		}
		router = sql.NullInt64{Int64: rid, Valid: ok}
	}
	if len(e) == 0 {
		var err error
		if id == 0 {
			_, err = s.queries.CreateODP(r.Context(), db.CreateODPParams{Name: v["name"], Coordinates: coords, Address: v["address"],
				PortAmount: ports, Attenuation: v["attenuation"], Coverage: cover, Description: v["description"], RouterID: router})
		} else {
			err = s.queries.UpdateODP(r.Context(), db.UpdateODPParams{Name: v["name"], Coordinates: coords, Address: v["address"],
				PortAmount: ports, Attenuation: v["attenuation"], Coverage: cover, Description: v["description"], RouterID: router, ID: id})
		}
		switch {
		case isUnique(err):
			e["name"] = "Name already exists"
		case isFK(err):
			e["router_id"] = "Invalid value"
		case err != nil:
			s.fail(w, "save odp", err)
			return
		default:
			act, msg := "odp.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "odp.update", "Data Updated Successfully"
			}
			s.done(w, r, "/admin/odp", msg, act, v["name"])
			return
		}
	}
	rs, ok := s.odpRouters(w, r)
	if !ok {
		return
	}
	action, head := "/admin/odp", "Add ODP"
	if id != 0 {
		action, head = fmt.Sprint("/admin/odp/", id), "Edit ODP"
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/odp", odpFields(v, e, rs)})
}

func (s *Server) odpDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if o, err := s.queries.GetODP(r.Context(), pathID(r)); err == nil {
		name = o.Name
	}
	s.remove(w, r, "/admin/odp", "odp", "ODP is in use", name, func(id int64) error {
		return s.queries.DeleteODP(r.Context(), id)
	})
}

// orZero turns an empty optional number into "0".
func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}
