package web

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

var bwNames = []string{"name", "rate_down", "rate_down_unit", "rate_up", "rate_up_unit", "burst"}

func bwFields(v, e map[string]string) []field {
	return append(section([]field{text("name", "Bandwidth Name", v, e).req()}, "Basic", ""), section([]field{
		text("rate_down", "Rate Download", v, e).as("number").req(),
		text("rate_down_unit", "Unit", v, e).opts("Kbps", "Mbps"),
		text("rate_up", "Rate Upload", v, e).as("number").req(),
		text("rate_up_unit", "Unit", v, e).opts("Kbps", "Mbps"),
		text("burst", "Burst", v, e).hint("MikroTik burst limit, optional"),
	}, "Speed", "")...)
}

func (s *Server) bwList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchBandwidths(r.Context(), db.SearchBandwidthsParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list bandwidths", err)
		return
	}
	lp := listPage{Heading: "Bandwidth", Base: "/admin/bandwidth", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Cols: []string{"Name", "Download", "Upload", "Burst"}}
	for _, b := range rows {
		lp.Rows = append(lp.Rows, listRow{b.ID, []string{b.Name, fmt.Sprint(b.RateDown, " ", b.RateDownUnit), fmt.Sprint(b.RateUp, " ", b.RateUpUnit), b.Burst}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) bwNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"rate_down_unit": "Mbps", "rate_up_unit": "Mbps"}
	s.renderForm(w, r, 200, formPage{"Add Bandwidth", "/admin/bandwidth", "/admin/bandwidth", bwFields(v, nil)})
}

func (s *Server) bwEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	b, err := s.queries.GetBandwidth(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get bandwidth", err)
		return
	}
	v := map[string]string{"name": b.Name, "rate_down": fmt.Sprint(b.RateDown), "rate_down_unit": b.RateDownUnit,
		"rate_up": fmt.Sprint(b.RateUp), "rate_up_unit": b.RateUpUnit, "burst": b.Burst}
	s.renderForm(w, r, 200, formPage{"Edit Bandwidth", fmt.Sprint("/admin/bandwidth/", id), "/admin/bandwidth", bwFields(v, nil)})
}

// bwSave serves both create (no {id} in the path) and update.
func (s *Server) bwSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, bwNames...)
	e := map[string]string{}
	if v["name"] == "" {
		e["name"] = "This field is required"
	}
	down, ok := posInt(v["rate_down"])
	if !ok {
		e["rate_down"] = "Enter a number greater than 0"
	}
	up, ok := posInt(v["rate_up"])
	if !ok {
		e["rate_up"] = "Enter a number greater than 0"
	}
	if !oneOf(v["rate_down_unit"], "Kbps", "Mbps") {
		e["rate_down_unit"] = "Invalid value"
	}
	if !oneOf(v["rate_up_unit"], "Kbps", "Mbps") {
		e["rate_up_unit"] = "Invalid value"
	}
	if len(e) == 0 {
		var err error
		if id == 0 {
			_, err = s.queries.CreateBandwidth(r.Context(), db.CreateBandwidthParams{Name: v["name"], RateDown: down, RateDownUnit: v["rate_down_unit"],
				RateUp: up, RateUpUnit: v["rate_up_unit"], Burst: v["burst"]})
		} else {
			err = s.queries.UpdateBandwidth(r.Context(), db.UpdateBandwidthParams{Name: v["name"], RateDown: down, RateDownUnit: v["rate_down_unit"],
				RateUp: up, RateUpUnit: v["rate_up_unit"], Burst: v["burst"], ID: id})
		}
		switch {
		case isUnique(err):
			e["name"] = "Name already exists"
		case err != nil:
			s.fail(w, "save bandwidth", err)
			return
		default:
			act, msg := "bandwidth.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "bandwidth.update", "Data Updated Successfully"
			}
			s.done(w, r, "/admin/bandwidth", msg, act, v["name"])
			return
		}
	}
	action, head := "/admin/bandwidth", "Add Bandwidth"
	if id != 0 {
		action, head = fmt.Sprint("/admin/bandwidth/", id), "Edit Bandwidth"
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/bandwidth", bwFields(v, e)})
}

func (s *Server) bwDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if b, err := s.queries.GetBandwidth(r.Context(), pathID(r)); err == nil {
		name = b.Name
	}
	s.remove(w, r, "/admin/bandwidth", "bandwidth", "Bandwidth is used by a plan", name, func(id int64) error {
		return s.queries.DeleteBandwidth(r.Context(), id)
	})
}
