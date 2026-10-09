package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

const perPage = 20

// field describes one form input; the shared "form" template renders a []field.
type field struct {
	Name, Label, Type, Value, Error, Hint string
	Options                               []option
	Required, Checked                     bool
	Section, SectionShow                  string // form card title, and the Alpine condition showing the whole card
	Show                                  string // Alpine condition showing this field
	Bind                                  bool   // field feeds the form's Alpine state (x-model)
	Gen                                   bool   // "Generate" button fills the field with a random value
}

type option struct{ Value, Label string }

type formPage struct {
	Heading, Action, Cancel string
	Fields                  []field
}

// group is a titled card of fields on the form page.
type group struct {
	Title, Show string
	Fields      []field
}

// Groups splits the fields into cards by consecutive Section.
func (fp formPage) Groups() []group {
	var gs []group
	for _, f := range fp.Fields {
		if n := len(gs); n == 0 || gs[n-1].Title != f.Section {
			gs = append(gs, group{Title: f.Section, Show: f.SectionShow})
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

type listRow struct {
	ID    int64
	Cells []string
}

// listPage is the shared list view. Base is the URL prefix, e.g. /admin/bandwidth.
type listPage struct {
	Heading, Base, Q string
	Cols             []string
	Rows             []listRow
	CanCreate        bool // shows the "Add" button
	CanEdit          bool // shows edit and delete
	ViewLink         bool // first cell links to Base/ID instead of Base/ID/edit
	Searchable       bool
	Prev, Next       int          // page numbers, 0 = none
	DeleteOnly       bool         // rows have no edit page
	Links            []option     // extra toolbar buttons: Value = href
	Filters          []filter     // selects next to the search box
	SortKeys         []string     // parallel to Cols; "" = not sortable
	Sort, Dir        string       // current sort key and "asc"/"desc"
	InvoiceLink      bool         // first cell links to Base/ID/invoice
	NoDelete         bool         // rows have no delete button
	Actions          []rowAction  // per-row POST buttons
	Dates            bool         // from/to date inputs next to the search box
	From, To         string       // YYYY-MM-DD
	Bulk             []bulkAction // row checkboxes + buttons posting the selected ids to Base/Path
	Clean            string       // POST URL of the "keep N days / Clean logs" form; "" = none
}

// bulkAction is a button that POSTs the checked row ids (name "ids") to Base/Path.
type bulkAction struct {
	Path, Label string
	Danger      bool
	All         bool // acts on all matching rows, ignores the selection
}

const maxBulkIDs = 1000

// parseIDs reads the posted "ids" strictly: positive integers only, duplicates dropped, 1..maxBulkIDs of them.
func parseIDs(r *http.Request) ([]int64, bool) {
	r.ParseForm()
	vals := r.PostForm["ids"]
	if len(vals) == 0 || len(vals) > maxBulkIDs {
		return nil, false
	}
	seen := map[int64]bool{}
	var ids []int64
	for _, v := range vals {
		id, ok := posInt(v)
		if !ok {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, true
}

// bulkDelete runs del over the posted ids in ONE transaction, logs once and flashes "N Data Deleted Successfully".
// A foreign-key refusal rolls everything back and shows inUse.
func (s *Server) bulkDelete(w http.ResponseWriter, r *http.Request, back, what, inUse string, del func(q *db.Queries, ids []int64) (int64, error)) {
	ids, ok := parseIDs(r)
	if !ok {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Select at least one row"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	tx, err := s.conn.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, "begin bulk delete "+what, err)
		return
	}
	defer tx.Rollback()
	n, err := del(s.queries.WithTx(tx), ids)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if !isFK(err) {
			s.fail(w, "bulk delete "+what, err)
			return
		}
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.bulkDone(w, r, back, what+".delete_many", n)
}

// bulkDone logs one activity entry with the count and flashes "N Data Deleted Successfully".
func (s *Server) bulkDone(w http.ResponseWriter, r *http.Request, back, action string, n int64) {
	s.logActivity(r, action, fmt.Sprint(n))
	s.sessions.Put(r.Context(), "flash", fmt.Sprintf("%d %s", n, s.catalog.T(s.language(), "Data Deleted Successfully")))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// listSort reads ?sort and ?dir against a whitelist. param is "key_dir" for the SQL CASE ("" = default order).
func listSort(r *http.Request, allowed ...string) (sort, dir, param string) {
	g := r.URL.Query().Get
	dir = "asc"
	if !oneOf(g("sort"), allowed...) {
		return "", dir, ""
	}
	if g("dir") == "desc" {
		dir = "desc"
	}
	return g("sort"), dir, g("sort") + "_" + dir
}

type filter struct {
	Name, Val string
	Opts      []option
}

// rowAction is a per-row POST button to Base/ID/Path; Field adds a number input of that name.
type rowAction struct {
	Path, Label, Field string
	Confirm            bool // asks "Label name?" first
}

func (lp listPage) query() url.Values {
	v := url.Values{}
	if lp.Q != "" {
		v.Set("q", lp.Q)
	}
	if lp.From != "" {
		v.Set("from", lp.From)
	}
	if lp.To != "" {
		v.Set("to", lp.To)
	}
	for _, f := range lp.Filters {
		if f.Val != "" {
			v.Set(f.Name, f.Val)
		}
	}
	if lp.Sort != "" {
		v.Set("sort", lp.Sort)
		v.Set("dir", lp.Dir)
	}
	return v
}

// SortKey is the sort key of column i, "" when it is not sortable.
func (lp listPage) SortKey(i int) string {
	if i < len(lp.SortKeys) {
		return lp.SortKeys[i]
	}
	return ""
}

// PageURL is the list URL for a page, keeping the search, filters and sort.
func (lp listPage) PageURL(page int) string {
	v := lp.query()
	v.Set("page", strconv.Itoa(page))
	return lp.Base + "?" + v.Encode()
}

// SortURL is the header link for a column: the same sort flips direction.
func (lp listPage) SortURL(key string) string {
	v := lp.query()
	v.Set("sort", key)
	v.Set("dir", "asc")
	if lp.Sort == key && lp.Dir == "asc" {
		v.Set("dir", "desc")
	}
	return lp.Base + "?" + v.Encode()
}

func text(name, label string, v, e map[string]string) field {
	return field{Name: name, Label: label, Type: "text", Value: v[name], Error: e[name]}
}

func (f field) as(typ string) field { f.Type = typ; return f }
func (f field) req() field          { f.Required = true; return f }
func (f field) show(c string) field { f.Show = c; return f }
func (f field) bind() field         { f.Bind = true; return f }
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

// posInt parses a positive integer.
func posInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

func oneOf(v string, list ...string) bool { return contains(list, v) }

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
func isFK(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	ref := make([]byte, 4)
	rand.Read(ref)
	slog.Error(what, "err", err, "ref", hex.EncodeToString(ref))
	s.errorPage(w, hex.EncodeToString(ref))
}

// errorPage is the 500 page: Indonesian text from the catalog, no internal error text, a reference to find the log line.
// It does not use the page layout because fail has no request (and the failure may be in the layout itself).
func (s *Server) errorPage(w http.ResponseWriter, ref string) {
	t := func(k string) string { return html.EscapeString(s.catalog.T(s.language(), k)) }
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%[1]s</title><link rel="stylesheet" href="/static/app.css"></head>
<body><main class="mx-auto max-w-md p-4"><div class="card mt-12"><div class="card-body space-y-4" role="alert"><h1 class="text-xl font-semibold">%[1]s</h1><p>%[2]s</p><p class="hint">Ref: %[3]s</p>
<a class="btn btn-primary min-h-10" href="/" onclick="history.back();return false">%[4]s</a></div></div></main></body></html>`,
		t("Something went wrong"), t("The request could not be completed. Check the data before trying again."), ref, t("Go back"))
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// paging reads ?q and ?page. limit is perPage+1 so finish can tell if a next page exists.
func paging(r *http.Request) (q string, page int, limit, offset int64) {
	q = strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	return q, page, perPage + 1, int64(page-1) * perPage
}

// finish drops the extra row and sets Prev/Next.
func (lp *listPage) finish(page int) {
	if len(lp.Rows) > perPage {
		lp.Rows = lp.Rows[:perPage]
		lp.Next = page + 1
	}
	if page > 1 {
		lp.Prev = page - 1
	}
}

func (s *Server) renderList(w http.ResponseWriter, r *http.Request, lp listPage) {
	s.render(w, r, http.StatusOK, "list", Page{
		Title: lp.Heading,
		Flash: s.sessions.PopString(r.Context(), "flash"),
		Error: s.sessions.PopString(r.Context(), "error"),
		Data:  lp,
	})
}

func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, status int, fp formPage) {
	s.render(w, r, status, "form", Page{Title: fp.Heading, Data: fp})
}

// logActivity records an admin action. A failure is logged, never shown.
func (s *Server) logActivity(r *http.Request, action, desc string) {
	err := s.queries.CreateActivityLog(r.Context(), db.CreateActivityLogParams{
		ActorType: "admin", ActorID: adminFrom(r).ID, Action: action, Description: desc, Ip: clientIP(r),
	})
	if err != nil {
		slog.Error("activity log", "err", err)
	}
}

// done flashes a message, logs the action and redirects.
func (s *Server) done(w http.ResponseWriter, r *http.Request, back, msg, action, desc string) {
	s.logActivity(r, action, desc)
	s.sessions.Put(r.Context(), "flash", s.catalog.T(s.language(), msg))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// remove runs a delete; a foreign-key refusal becomes the friendly message inUse.
func (s *Server) remove(w http.ResponseWriter, r *http.Request, back, what, inUse, name string, del func(id int64) error) {
	if err := del(pathID(r)); err != nil {
		if !isFK(err) {
			s.fail(w, "delete "+what, err)
			return
		}
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), inUse))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.done(w, r, back, "Data Deleted Successfully", what+".delete", name)
}
