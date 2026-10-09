package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// Old customfield.php: admin-defined fields, values stored per customer.

var cfTypes = []string{"text", "number", "select", "date"}

var cfNames = []string{"name", "type", "options", "required", "sort_order"}

// cfKey is the form input name of a custom field value.
func cfKey(id int64) string { return fmt.Sprint("cf_", id) }

// cfOptions splits the comma-separated options of a select field.
func cfOptions(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

func cfFields(v, e map[string]string) []field {
	req := text("required", "Required", v, e).as("checkbox")
	req.Checked = v["required"] == "1"
	return section([]field{
		text("name", "Field Name", v, e).req(),
		text("type", "Type", v, e).opts(cfTypes...).req(),
		text("options", "Options", v, e).hint("For Select type: comma separated, e.g. Gold, Silver"),
		req,
		text("sort_order", "Order", v, e).as("number").hint("Lower numbers show first."),
	}, "Custom Field", "")
}

func (s *Server) cfList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListCustomFields(r.Context())
	if err != nil {
		s.fail(w, "list custom fields", err)
		return
	}
	lp := listPage{Heading: "Custom Fields", Base: "/admin/fields", CanCreate: true, CanEdit: true,
		Cols: []string{"Name", "Type", "Options", "Required", "Order"}, Links: []option{{"/admin/pages/announcement", "Pages"}}}
	for _, f := range rows {
		req := "No"
		if f.Required == 1 {
			req = "Yes"
		}
		lp.Rows = append(lp.Rows, listRow{f.ID, []string{f.Name, f.Type, f.Options, req, fmt.Sprint(f.SortOrder)}})
	}
	s.renderList(w, r, lp)
}

func (s *Server) cfNew(w http.ResponseWriter, r *http.Request) {
	v := map[string]string{"type": "text", "sort_order": "0"}
	s.renderForm(w, r, 200, formPage{"Add Custom Field", "/admin/fields", "/admin/fields", cfFields(v, nil)})
}

func (s *Server) cfEdit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	f, err := s.queries.GetCustomField(r.Context(), id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "get custom field", err)
		return
	}
	v := map[string]string{"name": f.Name, "type": f.Type, "options": f.Options, "sort_order": fmt.Sprint(f.SortOrder)}
	if f.Required == 1 {
		v["required"] = "1"
	}
	s.renderForm(w, r, 200, formPage{"Edit Custom Field", fmt.Sprint("/admin/fields/", id), "/admin/fields", cfFields(v, nil)})
}

// cfSave serves both create (no {id} in the path) and update.
func (s *Server) cfSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v := formVals(r, cfNames...)
	e := map[string]string{}
	if v["name"] == "" {
		e["name"] = "This field is required"
	}
	if !oneOf(v["type"], cfTypes...) {
		e["type"] = "Invalid value"
	}
	if v["type"] == "select" && len(cfOptions(v["options"])) == 0 {
		e["options"] = "Options are required for Select type"
	}
	order := int64(0)
	if v["sort_order"] != "" {
		n, err := strconv.ParseInt(v["sort_order"], 10, 64)
		if err != nil {
			e["sort_order"] = "Enter a whole number"
		}
		order = n
	}
	req := int64(0)
	if v["required"] == "1" {
		req = 1
	}
	if len(e) == 0 {
		var err error
		if id == 0 {
			_, err = s.queries.CreateCustomField(r.Context(), db.CreateCustomFieldParams{Name: v["name"], Type: v["type"],
				Options: v["options"], Required: req, SortOrder: order})
		} else {
			err = s.queries.UpdateCustomField(r.Context(), db.UpdateCustomFieldParams{Name: v["name"], Type: v["type"],
				Options: v["options"], Required: req, SortOrder: order, ID: id})
		}
		switch {
		case isUnique(err):
			e["name"] = "Name already exists"
		case err != nil:
			s.fail(w, "save custom field", err)
			return
		default:
			act, msg := "field.create", "Data Created Successfully"
			if id != 0 {
				act, msg = "field.update", "Data Updated Successfully"
			}
			s.done(w, r, "/admin/fields", msg, act, v["name"])
			return
		}
	}
	action, head := "/admin/fields", "Add Custom Field"
	if id != 0 {
		action, head = fmt.Sprint("/admin/fields/", id), "Edit Custom Field"
	}
	s.renderForm(w, r, http.StatusUnprocessableEntity, formPage{head, action, "/admin/fields", cfFields(v, e)})
}

func (s *Server) cfDelete(w http.ResponseWriter, r *http.Request) {
	name := ""
	if f, err := s.queries.GetCustomField(r.Context(), pathID(r)); err == nil {
		name = f.Name
	}
	s.remove(w, r, "/admin/fields", "field", "Custom field is in use", name, func(id int64) error {
		return s.queries.DeleteCustomField(r.Context(), id)
	})
}

// cfFormFields is the custom-field load hook: one input per field, filled from v
// (posted values) and, for a saved customer (custID != 0), from the stored values.
// The same list feeds the customer detail page.
func (s *Server) cfFormFields(ctx context.Context, custID int64, v, e map[string]string) ([]field, error) {
	defs, err := s.queries.ListCustomFields(ctx)
	if err != nil {
		return nil, err
	}
	stored := map[int64]string{}
	if custID != 0 {
		vals, err := s.queries.ListCustomerFieldValues(ctx, custID)
		if err != nil {
			return nil, err
		}
		for _, x := range vals {
			stored[x.FieldID] = x.Value
		}
	}
	var fs []field
	for _, d := range defs {
		k := cfKey(d.ID)
		if _, ok := v[k]; !ok {
			v[k] = stored[d.ID]
		}
		f := field{Name: k, Label: d.Name, Type: d.Type, Value: v[k], Error: e[k], Required: d.Required == 1}
		if d.Type == "select" {
			f.Options = anyOpts("-", cfOptions(d.Options)...)
		}
		fs = append(fs, f)
	}
	return section(fs, "Custom Fields", ""), nil
}

type cfVal struct {
	FieldID int64
	Value   string
}

// cfCheck is the custom-field save hook: it reads and validates the posted values
// into v and e, and returns what to store. Nothing is written here.
// ponytail: saved after the customer row, not in one transaction; a crash between the two leaves the customer without new values.
func (s *Server) cfCheck(ctx context.Context, r *http.Request, v, e map[string]string) ([]cfVal, error) {
	defs, err := s.queries.ListCustomFields(ctx)
	if err != nil {
		return nil, err
	}
	var out []cfVal
	for _, d := range defs {
		k := cfKey(d.ID)
		val := strings.TrimSpace(r.PostFormValue(k))
		v[k] = val
		switch {
		case val == "":
			if d.Required == 1 {
				e[k] = "This field is required"
			}
		case len(val) > 255:
			e[k] = "Maximum 255 characters"
		case d.Type == "number":
			if _, err := strconv.ParseFloat(val, 64); err != nil {
				e[k] = "Enter a number"
			}
		case d.Type == "date":
			if _, err := time.Parse("2006-01-02", val); err != nil {
				e[k] = "Enter a valid date"
			}
		case d.Type == "select":
			if !oneOf(val, cfOptions(d.Options)...) {
				e[k] = "Invalid value"
			}
		}
		out = append(out, cfVal{d.ID, val})
	}
	return out, nil
}

// cfStore writes the values of a saved customer.
func (s *Server) cfStore(ctx context.Context, custID int64, vals []cfVal) error {
	for _, x := range vals {
		if err := s.queries.UpsertCustomerFieldValue(ctx, db.UpsertCustomerFieldValueParams{CustomerID: custID, FieldID: x.FieldID, Value: x.Value}); err != nil {
			return err
		}
	}
	return nil
}

// customerForm is the customer form: the built-in fields followed by the custom ones.
func (s *Server) customerForm(ctx context.Context, id int64, v, e map[string]string, editing bool) ([]field, error) {
	cf, err := s.cfFormFields(ctx, id, v, e)
	if err != nil {
		return nil, err
	}
	return append(custFields(v, e, editing), cf...), nil
}
