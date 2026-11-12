package billing

// Coupon CRUD.

import (
	"database/sql"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"fmt"
	"strconv"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

var cpnKeys = []string{"code", "type", "value", "description", "max_usage", "min_order", "max_discount", "start_date", "end_date", "status"}

func cpnFields(v, e map[string]string) []field {
	return section([]field{
		text("code", "Coupon Code", v, e).WithHint("Leave empty to generate a random code"),
		text("type", "Type", v, e).Opts("fixed", "percent"),
		text("value", "Discount Value", v, e).As("number").Req(),
		text("description", "Description", v, e).As("textarea"),
		text("max_usage", "Max Usage", v, e).As("number").WithHint("0 = unlimited"),
		text("min_order", "Minimum Order Amount", v, e).As("number"),
		text("max_discount", "Max Discount Amount", v, e).As("number").WithHint("Percent coupons only, 0 = no cap"),
		text("start_date", "Start Date", v, e).As("date").Req(),
		text("end_date", "End Date", v, e).As("date").Req(),
		text("status", "Status", v, e).Opts("active", "inactive"),
	}, "Coupon", "")
}

func (s *Handlers) cpnList(w http.ResponseWriter, r *http.Request) {
	q, pg := view.Paging(r)
	rows, err := s.Queries.SearchCoupons(r.Context(), db.SearchCouponsParams{Q: q, PageLimit: pg.Limit(), PageOffset: pg.Offset()})
	if err != nil {
		s.Fail(w, "list coupons", err)
		return
	}
	lp := listPage{Heading: "Coupons", Base: "/admin/coupons", Q: q, Searchable: true, CanCreate: true, CanEdit: true,
		Bulk:    []bulkAction{{Path: "delete-many", Label: "Delete selected", Danger: true, All: false, Page: false}},
		Actions: []rowAction{{Path: "toggle", Label: "Block/Unblock", Field: "", Confirm: true}},
		Cols:    []string{"Code", "Type", "Value", "Max Usage", "Used", "Min Order", "Start Date", "End Date", "Status"}}
	for _, c := range rows {
		lp.Rows = append(lp.Rows, listRow{ID: c.ID, Cells: []string{c.Code, c.Type, fmt.Sprint(c.Value), fmt.Sprint(c.MaxUsage),
			fmt.Sprint(c.Used), fmt.Sprint(c.MinOrder), c.StartDate, c.EndDate, c.Status}})
	}
	total, err := s.Queries.CountCoupons(r.Context(), q)
	if err != nil {
		s.Fail(w, "count coupons", err)
		return
	}
	lp.Finish(pg, total)
	s.RenderList(w, r, lp)
}

func (s *Handlers) cpnNew(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006-01-02")
	v := map[string]string{"type": "fixed", "status": "active", "start_date": today, "max_usage": "0", "min_order": "0", "max_discount": "0"}
	s.RenderForm(w, r, 200, formPage{Heading: "Add Coupon", Action: "/admin/coupons", Cancel: "/admin/coupons", Fields: cpnFields(v, nil)})
}

func (s *Handlers) cpnEdit(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	c, err := s.Queries.GetCoupon(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.Fail(w, "get coupon", err)
		return
	}
	v := map[string]string{"code": c.Code, "type": c.Type, "value": fmt.Sprint(c.Value), "description": c.Description,
		"max_usage": fmt.Sprint(c.MaxUsage), "min_order": fmt.Sprint(c.MinOrder), "max_discount": fmt.Sprint(c.MaxDiscount),
		"start_date": c.StartDate, "end_date": c.EndDate, "status": c.Status}
	s.RenderForm(w, r, 200, formPage{Heading: "Edit Coupon", Action: fmt.Sprint("/admin/coupons/", id), Cancel: "/admin/coupons", Fields: cpnFields(v, nil)})
}

func (s *Handlers) cpnSave(w http.ResponseWriter, r *http.Request) {
	id := httpx.PathID(r)
	v := httpx.FormVals(r, cpnKeys...)
	e := map[string]string{}
	if v["code"] == "" {
		v["code"], _ = randomCode(UpperChars, 8)
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
			_, err = s.Queries.CreateCoupon(r.Context(), db.CreateCouponParams{Code: v["code"], Type: v["type"], Value: n["value"],
				Description: v["description"], MaxUsage: n["max_usage"], MinOrder: n["min_order"], MaxDiscount: n["max_discount"],
				StartDate: v["start_date"], EndDate: v["end_date"], Status: v["status"]})
		} else {
			err = s.Queries.UpdateCoupon(r.Context(), db.UpdateCouponParams{Code: v["code"], Type: v["type"], Value: n["value"],
				Description: v["description"], MaxUsage: n["max_usage"], MinOrder: n["min_order"], MaxDiscount: n["max_discount"],
				StartDate: v["start_date"], EndDate: v["end_date"], Status: v["status"], ID: id})
		}
		switch {
		case httpx.IsUnique(err):
			e["code"] = "Coupon Code already exists"
		case err != nil:
			s.Fail(w, "save coupon", err)
			return
		default:
			act, msg := "coupon.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "coupon.update", "Data Updated Successfully"
			}
			s.Done(w, r, "/admin/coupons", msg, act, v["code"])
			return
		}
	}
	action, head := "/admin/coupons", "Add Coupon"
	if id != 0 {
		action, head = fmt.Sprint("/admin/coupons/", id), "Edit Coupon"
	}
	s.RenderForm(w, r, http.StatusUnprocessableEntity, formPage{Heading: head, Action: action, Cancel: "/admin/coupons", Fields: cpnFields(v, e)})
}

func (s *Handlers) cpnToggle(w http.ResponseWriter, r *http.Request) {
	if err := s.Queries.ToggleCoupon(r.Context(), httpx.PathID(r)); err != nil {
		s.Fail(w, "toggle coupon", err)
		return
	}
	s.Done(w, r, "/admin/coupons", "Data Updated Successfully", "coupon.toggle", fmt.Sprint(httpx.PathID(r)))
}

func (s *Handlers) cpnDelete(w http.ResponseWriter, r *http.Request) {
	c, _ := s.Queries.GetCoupon(r.Context(), httpx.PathID(r))
	s.Remove(w, r, "/admin/coupons", "coupon", "Coupon is in use", c.Code, func(id int64) error {
		return s.Queries.DeleteCoupon(r.Context(), id)
	})
}

// cpnDeleteMany deletes the selected coupons (old PHP coupons delete, SuperAdmin/Admin/Sales).
func (s *Handlers) cpnDeleteMany(w http.ResponseWriter, r *http.Request) {
	s.BulkDelete(w, r, "/admin/coupons", "coupon", "Coupon is in use", func(q *db.Queries, ids []int64) (int64, error) {
		return q.DeleteCouponsByIDs(r.Context(), ids)
	})
}
