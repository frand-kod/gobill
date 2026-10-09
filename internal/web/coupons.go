package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

var cpnKeys = []string{"code", "type", "value", "description", "max_usage", "min_order", "max_discount", "start_date", "end_date", "status"}

func cpnFields(v, e map[string]string) []field {
	return section([]field{
		text("code", "Coupon Code", v, e).hint("Leave empty to generate a random code"),
		text("type", "Type", v, e).opts("fixed", "percent"),
		text("value", "Discount Value", v, e).as("number").req(),
		text("description", "Description", v, e).as("textarea"),
		text("max_usage", "Max Usage", v, e).as("number").hint("0 = unlimited"),
		text("min_order", "Minimum Order Amount", v, e).as("number"),
		text("max_discount", "Max Discount Amount", v, e).as("number").hint("Percent coupons only, 0 = no cap"),
		text("start_date", "Start Date", v, e).as("date").req(),
		text("end_date", "End Date", v, e).as("date").req(),
		text("status", "Status", v, e).opts("active", "inactive"),
	}, "Coupon", "")
}

func (s *Server) cpnList(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	rows, err := s.queries.SearchCoupons(r.Context(), db.SearchCouponsParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list coupons", err)
		return
	}
	lp := listPage{Heading: "Coupons", Base: "/admin/coupons", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Bulk:    []bulkAction{{"delete-many", "Delete selected", true, false}},
		Actions: []rowAction{{"toggle", "Block/Unblock", "", true}},
		Cols:    []string{"Code", "Type", "Value", "Max Usage", "Used", "Min Order", "Start Date", "End Date", "Status"}}
	for _, c := range rows {
		lp.Rows = append(lp.Rows, listRow{c.ID, []string{c.Code, c.Type, fmt.Sprint(c.Value), fmt.Sprint(c.MaxUsage),
			fmt.Sprint(c.Used), fmt.Sprint(c.MinOrder), c.StartDate, c.EndDate, c.Status}})
	}
	lp.finish(page)
	s.renderList(w, r, lp)
}

func (s *Server) cpnNew(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006-01-02")
	v := map[string]string{"type": "fixed", "status": "active", "start_date": today, "max_usage": "0", "min_order": "0", "max_discount": "0"}
	s.renderForm(w, r, 200, formPage{"Add Coupon", "/admin/coupons", "/admin/coupons", cpnFields(v, nil)})
}

func (s *Server) cpnEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	c, err := s.queries.GetCoupon(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get coupon", err)
		return
	}
	v := map[string]string{"code": c.Code, "type": c.Type, "value": fmt.Sprint(c.Value), "description": c.Description,
		"max_usage": fmt.Sprint(c.MaxUsage), "min_order": fmt.Sprint(c.MinOrder), "max_discount": fmt.Sprint(c.MaxDiscount),
		"start_date": c.StartDate, "end_date": c.EndDate, "status": c.Status}
	s.renderForm(w, r, 200, formPage{"Edit Coupon", fmt.Sprint("/admin/coupons/", id), "/admin/coupons", cpnFields(v, nil)})
}

func (s *Server) cpnSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, cpnKeys...)
	e := map[string]string{}
	if v["code"] == "" {
		v["code"], _ = randomCode(upperChars, 8)
	} else if !prefixRe.MatchString(v["code"]) {
		e["code"] = "Invalid characters"
	}
	if v["type"] != "fixed" && v["type"] != "percent" {
		e["type"] = "Invalid"
	}
	if v["status"] != "active" && v["status"] != "inactive" {
		e["status"] = "Invalid"
	}
	n := map[string]int64{}
	for _, k := range []string{"value", "max_usage", "min_order", "max_discount"} {
		n[k], _ = strconv.ParseInt(v[k], 10, 64)
		if n[k] < 0 || k == "value" && n[k] < 1 {
			e[k] = "Invalid number"
		}
	}
	if v["type"] == "percent" && n["value"] > 100 {
		e["value"] = "Invalid number"
	}
	for _, k := range []string{"start_date", "end_date"} {
		if _, err := time.Parse("2006-01-02", v[k]); err != nil {
			e[k] = "Invalid date"
		}
	}
	if e["start_date"] == "" && e["end_date"] == "" && v["end_date"] < v["start_date"] {
		e["end_date"] = "Invalid date"
	}
	if len(e) == 0 {
		var err error
		if id == 0 {
			_, err = s.queries.CreateCoupon(r.Context(), db.CreateCouponParams{Code: v["code"], Type: v["type"], Value: n["value"],
				Description: v["description"], MaxUsage: n["max_usage"], MinOrder: n["min_order"], MaxDiscount: n["max_discount"],
				StartDate: v["start_date"], EndDate: v["end_date"], Status: v["status"]})
		} else {
			err = s.queries.UpdateCoupon(r.Context(), db.UpdateCouponParams{Code: v["code"], Type: v["type"], Value: n["value"],
				Description: v["description"], MaxUsage: n["max_usage"], MinOrder: n["min_order"], MaxDiscount: n["max_discount"],
				StartDate: v["start_date"], EndDate: v["end_date"], Status: v["status"], ID: id})
		}
		switch {
		case isUnique(err):
			e["code"] = "Coupon Code already exists"
		case err != nil:
			s.fail(w, "save coupon", err)
			return
		default:
			act, msg := "coupon.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "coupon.update", "Data Updated Successfully"
			}
			s.done(w, r, "/admin/coupons", msg, act, v["code"])
			return
		}
	}
	action, head := "/admin/coupons", "Add Coupon"
	if id != 0 {
		action, head = fmt.Sprint("/admin/coupons/", id), "Edit Coupon"
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/coupons", cpnFields(v, e)})
}

func (s *Server) cpnToggle(w http.ResponseWriter, r *http.Request) {
	if err := s.queries.ToggleCoupon(r.Context(), pathID(r)); err != nil {
		s.fail(w, "toggle coupon", err)
		return
	}
	s.done(w, r, "/admin/coupons", "Data Updated Successfully", "coupon.toggle", fmt.Sprint(pathID(r)))
}

func (s *Server) cpnDelete(w http.ResponseWriter, r *http.Request) {
	c, _ := s.queries.GetCoupon(r.Context(), pathID(r))
	s.remove(w, r, "/admin/coupons", "coupon", "Coupon is in use", c.Code, func(id int64) error {
		return s.queries.DeleteCoupon(r.Context(), id)
	})
}

// cpnDeleteMany deletes the selected coupons (old PHP coupons delete, SuperAdmin/Admin/Sales).
func (s *Server) cpnDeleteMany(w http.ResponseWriter, r *http.Request) {
	s.bulkDelete(w, r, "/admin/coupons", "coupon", "Coupon is in use", func(q *db.Queries, ids []int64) (int64, error) {
		return q.DeleteCouponsByIDs(r.Context(), ids)
	})
}
