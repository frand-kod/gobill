package web

// Form field definitions, field builders and form page rendering.

import (
	"html/template"
	"net/http"

	"strings"
)

// field describes one form input; the shared "form" template renders a []field.
type field struct {
	Name, Label, Type, Value, Error, Hint string
	Options                               []option
	Required, Checked                     bool
	Section, SectionShow                  string       // form card title, and the Alpine condition showing the whole card
	Show                                  string       // Alpine condition showing this field
	Bind                                  bool         // field feeds the form's Alpine state (x-model)
	Gen                                   bool         // "Generate" button fills the field with a random value
	Fold                                  bool         // its card sits inside a collapsed "Advanced options"
	Balance                               bool         // customer picker lists each customer's balance
	Btn                                   string       // link field: button text, default "Download backup"
	Snippet                               string       // tokenshow field: text shown in a code block
	Img                                   template.URL // qris field: preview of the stored code (data: URI)
	Inp                                   string       // test field: input type of the box above its button, "" = none
}

type option struct{ Value, Label string }

type formPage struct {
	Heading, Action, Cancel string
	Fields                  []field
}

// group is a titled card of fields on the form page.
type group struct {
	Title, Show string
	Fold        bool
	Fields      []field
}

// Open reports whether a folded card must start open: one of its fields has an error.
func (g group) Open() bool {
	for _, f := range g.Fields {
		if f.Error != "" {
			return true
		}
	}
	return false
}

// Groups splits the fields into cards by consecutive Section.
func (fp formPage) Groups() []group {
	var gs []group
	for _, f := range fp.Fields {
		if n := len(gs); n == 0 || gs[n-1].Title != f.Section {
			gs = append(gs, group{Title: f.Section, Show: f.SectionShow, Fold: f.Fold})
		}
		gs[len(gs)-1].Fields = append(gs[len(gs)-1].Fields, f)
	}
	return gs
}

// XData is the Alpine state: the current value of every Bind field.
func (fp formPage) XData() string {
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
	return jsonStr(m)
}

// section puts fields in one titled card; show is an optional Alpine condition for the card.
func section(fs []field, title, show string) []field {
	for i := range fs {
		fs[i].Section, fs[i].SectionShow = title, show
	}
	return fs
}

func text(name, label string, v, e map[string]string) field {
	return field{Name: name, Label: label, Type: "text", Value: v[name], Error: e[name]}
}

// customerPick is a username field shown as the searchable customer picker (type "customer").
func customerPick(name, label string, v, e map[string]string) field {
	return field{Name: name, Label: label, Type: "customer", Value: v[name], Error: e[name]}
}

func (f field) withBalance() field { f.Balance = true; return f }

// PickConfig is the Alpine state of the customer picker: the username already chosen (may be empty).
func (f field) PickConfig() string {
	return jsonStr(map[string]any{"value": f.Value, "balance": f.Balance})
}

func (f field) as(typ string) field { f.Type = typ; return f }

func (f field) req() field { f.Required = true; return f }

func (f field) show(c string) field { f.Show = c; return f }

func (f field) bind() field { f.Bind = true; return f }

func (f field) hint(h string) field { f.Hint = h; return f }

func (f field) opts(o ...string) field {
	f.Type = "select"
	for _, x := range o {
		f.Options = append(f.Options, option{x, x})
	}
	return f
}

// formVals returns the trimmed values of the named form fields.
func formVals(r *http.Request, names ...string) map[string]string {
	m := map[string]string{}
	for _, n := range names {
		m[n] = strings.TrimSpace(r.PostFormValue(n))
	}
	return m
}

func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, status int, fp formPage) {
	s.render(w, r, status, "form", Page{Title: fp.Heading, Data: fp})
}

// HasFile reports whether a field posts a file, so the form needs multipart encoding.
func (fp formPage) HasFile() bool {
	for _, f := range fp.Fields {
		if f.Type == "file" || f.Type == "qris" {
			return true
		}
	}
	return false
}
