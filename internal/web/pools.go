package web

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

// routerNames lists up to 1000 routers; pools are meaningless without one.
func (s *Server) routerNames(r *http.Request) ([]db.Router, error) {
	return s.queries.ListRouters(r.Context(), db.ListRoutersParams{Limit: 1000})
}

func poolFields(v, e map[string]string, routers []db.Router) []field {
	sel := text("router_id", "Router", v, e).as("select").req()
	for _, x := range routers {
		sel.Options = append(sel.Options, option{fmt.Sprint(x.ID), x.Name})
	}
	return append(section([]field{
		text("name", "Pool Name", v, e).req(),
		text("local_ip", "Local IP", v, e).hint("Optional"),
		text("range_ip", "IP Range", v, e).req().hint("e.g. 10.10.10.2-10.10.10.254"),
	}, "Pool", ""), section([]field{sel}, "Router", "")...)
}

func (s *Server) poolList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchPools(r.Context(), db.SearchPoolsParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list pools", err)
		return
	}
	routers, err := s.routerNames(r)
	if err != nil {
		s.fail(w, "list routers", err)
		return
	}
	names := map[int64]string{}
	for _, x := range routers {
		names[x.ID] = x.Name
	}
	lp := listPage{Heading: "Pool", Base: "/admin/pool", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Cols: []string{"Pool Name", "Local IP", "IP Range", "Router"}}
	for _, p := range rows {
		lp.Rows = append(lp.Rows, listRow{p.ID, []string{p.Name, p.LocalIp, p.RangeIp, names[p.RouterID]}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) poolNew(w http.ResponseWriter, r *http.Request) {
	routers, err := s.routerNames(r)
	if err != nil {
		s.fail(w, "list routers", err)
		return
	}
	s.renderForm(w, r, 200, formPage{"Add Pool", "/admin/pool", "/admin/pool", poolFields(nil, nil, routers)})
}

func (s *Server) poolEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	p, err := s.queries.GetPool(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get pool", err)
		return
	}
	routers, err := s.routerNames(r)
	if err != nil {
		s.fail(w, "list routers", err)
		return
	}
	v := map[string]string{"name": p.Name, "local_ip": p.LocalIp, "range_ip": p.RangeIp, "router_id": fmt.Sprint(p.RouterID)}
	s.renderForm(w, r, 200, formPage{"Edit Pool", fmt.Sprint("/admin/pool/", id), "/admin/pool", poolFields(v, nil, routers)})
}

func (s *Server) poolSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, "name", "local_ip", "range_ip", "router_id")
	e := map[string]string{}
	for _, k := range []string{"name", "range_ip"} {
		if v[k] == "" {
			e[k] = "This field is required"
		}
	}
	rid, _ := strconv.ParseInt(v["router_id"], 10, 64)
	if _, err := s.queries.GetRouter(r.Context(), rid); err != nil {
		e["router_id"] = "Choose a router"
	}
	if len(e) == 0 {
		var err error
		saved := db.Pool{ID: id, Name: v["name"], LocalIp: v["local_ip"], RangeIp: v["range_ip"], RouterID: rid}
		old, _ := s.queries.GetPool(r.Context(), id)
		if id == 0 {
			saved, err = s.queries.CreatePool(r.Context(), db.CreatePoolParams{Name: v["name"], LocalIp: v["local_ip"], RangeIp: v["range_ip"], RouterID: rid})
		} else {
			err = s.queries.UpdatePool(r.Context(), db.UpdatePoolParams{Name: v["name"], LocalIp: v["local_ip"], RangeIp: v["range_ip"], RouterID: rid, ID: id})
		}
		switch {
		case isUnique(err):
			e["name"] = "Name already exists"
		case err != nil:
			s.fail(w, "save pool", err)
			return
		default:
			act, msg, op := "pool.create", "Data Created Successfully", "add"
			if id != 0 {
				act, msg, op = "pool.update", "Data Updated Successfully", "update"
			}
			s.syncPool(r, op, saved, old.Name)
			s.done(w, r, "/admin/pool", msg, act, v["name"])
			return
		}
	}
	routers, err := s.routerNames(r)
	if err != nil {
		s.fail(w, "list routers", err)
		return
	}
	action, head := "/admin/pool", "Add Pool"
	if id != 0 {
		action, head = fmt.Sprint("/admin/pool/", id), "Edit Pool"
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/pool", poolFields(v, e, routers)})
}

// syncPool tells the pool's router about a change; a failure keeps the DB change and warns.
func (s *Server) syncPool(r *http.Request, op string, p db.Pool, oldName string) {
	if s.Billing == nil {
		return
	}
	if err := s.Billing.SyncPool(r.Context(), op, p, oldName); err != nil {
		slog.Error("sync pool", "op", op, "pool", p.Name, "err", err)
		s.logActivity(r, "pool.sync_failed", fmt.Sprintf("%s %s: %v", op, p.Name, err))
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Saved, but the router could not be updated")+": "+err.Error())
	}
}

func (s *Server) poolDelete(w http.ResponseWriter, r *http.Request) {
	p, _ := s.queries.GetPool(r.Context(), pathID(r))
	s.remove(w, r, "/admin/pool", "pool", "Pool is used by a plan", p.Name, func(id int64) error {
		if err := s.queries.DeletePool(r.Context(), id); err != nil {
			return err
		}
		s.syncPool(r, "remove", p, "")
		return nil
	})
}
