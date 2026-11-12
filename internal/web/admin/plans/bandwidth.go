package plans

// Bandwidth profile CRUD.

import (
	"database/sql"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"fmt"

	"github.com/frand-kod/gobill/internal/db"
)

var bwNames = []string{"name", "rate_down", "rate_down_unit", "rate_up", "rate_up_unit", "burst"}

func bwFields(v, e map[string]string) []field {
	return append(section([]field{text("name", "Bandwidth Name", v, e).Req()}, "Basic", ""), section([]field{
		text("rate_down", "Rate Download", v, e).As("number").Req(),
		text("rate_down_unit", "Unit", v, e).Opts("Kbps", "Mbps"),
		text("rate_up", "Rate Upload", v, e).As("number").Req(),
		text("rate_up_unit", "Unit", v, e).Opts("Kbps", "Mbps"),
		text("burst", "Burst", v, e),
	}, "Speed", "")...)
}

func (s *Handlers) bwList(w http.ResponseWriter, r *http.Request) {
	q, pg := view.Paging(r)
	_, isoBW := s.IsolirIDs(r.Context()) // the isolir profile is set in Settings > Isolir
	rows, err := s.Queries.SearchBandwidths(r.Context(), db.SearchBandwidthsParams{Q: q, PageLimit: pg.Limit(), PageOffset: pg.Offset(), HiddenID: isoBW})
	if err != nil {
		s.Fail(w, "list bandwidths", err)
		return
	}
	lp := listPage{Heading: "Bandwidth", Base: "/admin/bandwidth", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Cols: []string{"Name", "Download", "Upload", "Burst"}}
	for _, b := range rows {
		lp.Rows = append(lp.Rows, listRow{ID: b.ID, Cells: []string{b.Name, fmt.Sprint(b.RateDown, " ", b.RateDownUnit), fmt.Sprint(b.RateUp, " ", b.RateUpUnit), b.Burst}})
	}
	total, err := s.Queries.CountBandwidths(r.Context(), db.CountBandwidthsParams{Term: q, HiddenID: isoBW})
	if err != nil {
		s.Fail(w, "count bandwidths", err)
		return
	}
	lp.Finish(pg, total)
	s.RenderList(w, r, lp)
}

func (s *Handlers) bwNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"rate_down_unit": "Mbps", "rate_up_unit": "Mbps"}
	s.RenderForm(w, r, 200, formPage{Heading: "Add Bandwidth", Action: "/admin/bandwidth", Cancel: "/admin/bandwidth", Fields: bwFields(v, nil)})
}

func (s *Handlers) bwEdit(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	b, err := s.Queries.GetBandwidth(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get bandwidth", err)
		return
	}
	v := map[string]string{"name": b.Name, "rate_down": fmt.Sprint(b.RateDown), "rate_down_unit": b.RateDownUnit,
		"rate_up": fmt.Sprint(b.RateUp), "rate_up_unit": b.RateUpUnit, "burst": b.Burst}
	s.RenderForm(w, r, 200, formPage{Heading: "Edit Bandwidth", Action: fmt.Sprint("/admin/bandwidth/", id), Cancel: "/admin/bandwidth", Fields: bwFields(v, nil)})
}

// BwSave serves both create (no {id} in the path) and update; the setup wizard replays it too.
func (s *Handlers) BwSave(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	v := httpx.FormVals(r, bwNames...)
	e := map[string]string{}
	if v["name"] == "" {
		e["name"] = "This field is required"
	}
	down, ok := httpx.PosInt(v["rate_down"])
	if !ok {
		e["rate_down"] = "Enter a number greater than 0"
	}
	up, ok := httpx.PosInt(v["rate_up"])
	if !ok {
		e["rate_up"] = "Enter a number greater than 0"
	}
	if !httpx.OneOf(v["rate_down_unit"], "Kbps", "Mbps") {
		e["rate_down_unit"] = "Invalid value"
	}
	if !httpx.OneOf(v["rate_up_unit"], "Kbps", "Mbps") {
		e["rate_up_unit"] = "Invalid value"
	}
	if len(e) == 0 {
		var err error
		if id == 0 {
			_, err = s.Queries.CreateBandwidth(r.Context(), db.CreateBandwidthParams{Name: v["name"], RateDown: down, RateDownUnit: v["rate_down_unit"],
				RateUp: up, RateUpUnit: v["rate_up_unit"], Burst: v["burst"]})
		} else {
			err = s.Queries.UpdateBandwidth(r.Context(), db.UpdateBandwidthParams{Name: v["name"], RateDown: down, RateDownUnit: v["rate_down_unit"],
				RateUp: up, RateUpUnit: v["rate_up_unit"], Burst: v["burst"], ID: id})
		}
		switch {
		case httpx.IsUnique(err):
			e["name"] = "Name already exists"
		case err != nil:
			s.Fail(w, "save bandwidth", err)
			return
		default:
			act, msg := "bandwidth.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "bandwidth.update", "Data Updated Successfully"
			}
			s.Done(w, r, "/admin/bandwidth", msg, act, v["name"])
			return
		}
	}
	action, head := "/admin/bandwidth", "Add Bandwidth"
	if id != 0 {
		action, head = fmt.Sprint("/admin/bandwidth/", id), "Edit Bandwidth"
	}
	s.RenderForm(w, r, http.StatusUnprocessableEntity, formPage{Heading: head, Action: action, Cancel: "/admin/bandwidth", Fields: bwFields(v, e)})
}

func (s *Handlers) bwDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if b, err := s.Queries.GetBandwidth(r.Context(), httpx.PathID(r)); err == nil {
		name = b.Name
	}
	s.Remove(w, r, "/admin/bandwidth", "bandwidth", "Bandwidth is used by a plan", name, func(id int64) error {
		return s.Queries.DeleteBandwidth(r.Context(), id)
	})
}
