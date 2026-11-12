package adminx

// Form field definitions, Field builders and form page rendering.

import (
	"encoding/json"
	"github.com/frand-kod/gobill/internal/web/core"
	"html/template"
	"net/http"

	"strings"

	"github.com/frand-kod/gobill/internal/notify"
)

// Field describes one form input; the shared "form" template renders a []Field.
type Field struct {
	Name, Label, Type, Value, Error, Hint string
	Options                               []Option
	Required, Checked                     bool
	Section, SectionShow                  string       // form card title, and the Alpine condition showing the whole card
	Show                                  string       // Alpine condition showing this field
	Bind                                  bool         // Field feeds the form's Alpine state (x-model)
	Gen                                   bool         // "Generate" button fills the field with a random value
	Fold                                  bool         // its card sits inside a collapsed "Advanced options"
	Numeric                               bool         // number input: digit keypad on phones (inputmode=numeric)
	Balance                               bool         // customer picker lists each customer's balance
	Btn                                   string       // link field: button text, default "Download backup"
	Warn, WarnTo                          string       // select: confirm text asked on save when the value was changed to WarnTo
	Cur                                   bool         // password input holds the current password (autocomplete=current-password)
	Confirm                               string       // formbtn field: browser confirm text shown before the submit
	Snippet                               string       // tokenshow field: text shown in a code block
	Img                                   template.URL // qris field: preview of the stored code (data: URI)
	Inp                                   string       // test field: input type of the box above its button, "" = none
	Vars                                  []notify.Var // textarea: variables as chips that insert [[name]] at the cursor
	Reset                                 string       // textarea: POST URL of a "Reset to default" button under it, "" = none
	Wide                                  bool         // spans both columns of the card grid
	Parts                                 []Field      // row: the controls on one line under the row's label
	Class                                 string       // row part: extra classes on its control (width)
	ReqIf                                 string       // Alpine condition under which the field is required: shows the * and aria-required
	Chips                                 []string     // text input: example values as buttons that insert the text at the cursor
	Change                                string       // select: Alpine handler run on change
	Gate                                  string       // JS condition under which the field is submitted; set by Groups, "" = always
}

type FormPage struct {
	Heading, Action, Cancel string
	Fields                  []Field
}

// Group is a titled card of fields on the form page.
type Group struct {
	Title, Show string
	Fold        bool
	Fields      []Field
}

// Open reports whether a folded card must start open: one of its fields has an error.
func (g Group) Open() bool {
	for _, f := range g.Fields {
		if f.Error != "" {
			return true
		}
	}
	return false
}

// Groups splits the fields into cards by consecutive Section.
func (fp FormPage) Groups() []Group {
	var gs []Group
	for _, f := range fp.Fields {
		if n := len(gs); n == 0 || gs[n-1].Title != f.Section {
			gs = append(gs, Group{Title: f.Section, Show: f.SectionShow, Fold: f.Fold})
		}
		gs[len(gs)-1].Fields = append(gs[len(gs)-1].Fields, f)
	}
	// a field hidden by its condition (or its card's) is not submitted; one with an error, or in a card with one, stays visible
	for i := range gs {
		if gs[i].Open() {
			gs[i].Show = ""
		}
		for j := range gs[i].Fields {
			f := &gs[i].Fields[j]
			var c []string
			if gs[i].Show != "" {
				c = append(c, "("+gs[i].Show+")")
			}
			if f.Show != "" && f.Error == "" {
				c = append(c, "("+f.Show+")")
			}
			f.Gate = strings.Join(c, " && ")
			if f.Error != "" {
				f.Show = ""
			}
		}
	}
	return gs
}

// Errors lists the fields with an error, for the summary at the top of the form.
func (fp FormPage) Errors() []Field {
	var out []Field
	for _, f := range fp.Fields {
		if f.Error != "" && f.Type != "hidden" {
			out = append(out, f)
		}
	}
	return out
}

// Describe is the id list for aria-describedby: the hint and the error, when present.
func (f Field) Describe() string {
	var ids []string
	if f.Hint != "" {
		ids = append(ids, f.Name+"-hint")
	}
	if f.Error != "" {
		ids = append(ids, f.Name+"-err")
	}
	return strings.Join(ids, " ")
}

// XData is the Alpine state: the current value of every Bind field.
func (fp FormPage) XData() string {
	m := map[string]any{}
	for _, f := range fp.Fields {
		if f.Bind {
			if f.Type == "checkbox" {
				m[f.Name] = f.Checked
			} else {
				m[f.Name] = f.Value
			}
		}
	}
	return JSONStr(m)
}

// section puts fields in one titled card; show is an optional Alpine condition for the card.
func Section(fs []Field, title, show string) []Field {
	for i := range fs {
		fs[i].Section, fs[i].SectionShow = title, show
	}
	return fs
}

func Text(name, label string, v, e map[string]string) Field {
	return Field{Name: name, Label: label, Type: "text", Value: v[name], Error: e[name]}
}

// CustomerPick is a username field shown as the searchable customer picker (type "customer").
func CustomerPick(name, label string, v, e map[string]string) Field {
	return Field{Name: name, Label: label, Type: "customer", Value: v[name], Error: e[name]}
}

func (f Field) WithBalance() Field { f.Balance = true; return f }

// PickConfig is the Alpine state of the customer picker: the username already chosen (may be empty).
func (f Field) PickConfig() string {
	return JSONStr(map[string]any{"value": f.Value, "balance": f.Balance})
}

// WholeErr is the message for a whole-number field given a decimal ("12.5", "12,5"); "" for anything else.
func WholeErr(v string) string {
	if strings.ContainsAny(v, ".,") {
		return "Use whole numbers without dots or commas"
	}
	return ""
}

func (f Field) As(typ string) Field { f.Type = typ; return f }

func (f Field) Req() Field { f.Required = true; return f }

func (f Field) WithShow(c string) Field { f.Show = c; return f }

func (f Field) WithBind() Field { f.Bind = true; return f }

func (f Field) WithReqIf(c string) Field { f.ReqIf = c; return f }

func (f Field) WithChips(c ...string) Field { f.Chips = c; return f }

func (f Field) WithHint(h string) Field { f.Hint = h; return f }

func (f Field) WithWarn(text, to string) Field { f.Warn, f.WarnTo = text, to; return f }

func (f Field) WithWide() Field { f.Wide = true; return f }

func (f Field) WithClass(c string) Field { f.Class = c; return f }

// row puts controls on one line under one label; the row's hint and error are shown once for all of them.
func Row(name, label string, parts ...Field) Field {
	return Field{Name: name, Label: label, Type: "row", Parts: parts}
}

func (f Field) Opts(o ...string) Field {
	f.Type = "select"
	for _, x := range o {
		f.Options = append(f.Options, Option{x, x})
	}
	return f
}

func (s *Base) RenderForm(w http.ResponseWriter, r *http.Request, status int, fp FormPage) {
	s.Render(w, r, status, "form", core.Page{Title: fp.Heading, Data: fp})
}

// Submit is the submit button label: lookup and test pages run an action instead of saving.
func (fp FormPage) Submit() string {
	switch fp.Action {
	case "/admin/radius/ip-lookup":
		return "Search"
	case "/admin/radius/test-login":
		return "Run test"
	}
	return "Save"
}

// HasVars reports whether a field has variable chips, so the form loads their script.
func (fp FormPage) HasVars() bool {
	for _, f := range fp.Fields {
		if len(f.Vars) > 0 || len(f.Chips) > 0 {
			return true
		}
	}
	return false
}

// HasFile reports whether a field posts a file, so the form needs multipart encoding.
func (fp FormPage) HasFile() bool {
	for _, f := range fp.Fields {
		if f.Type == "file" || f.Type == "qris" {
			return true
		}
	}
	return false
}

// JSONStr is v as JSON text, for Alpine state in templates.
func JSONStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Option is one choice of a select or a toolbar link: Value is the form value or href.
type Option struct{ Value, Label string }
