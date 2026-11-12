package adminx

// List pages: sorting, paging, filters, row and bulk actions, list rendering.

import (
	"github.com/frand-kod/gobill/internal/web/core"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"
)

type ListRow struct {
	ID    int64
	Cells []string
}

// ListPage is the shared list view. Base is the URL prefix, e.g. /admin/bandwidth.
type ListPage struct {
	Heading, Base, Q string
	Cols             []string
	Rows             []ListRow
	CanCreate        bool // shows the "Add" button
	CanEdit          bool // shows edit and delete
	ViewLink         bool // first cell links to Base/ID instead of Base/ID/edit
	Searchable       bool
	Nav              view.PageNav // row count, page-size links and Prev/Next under the table
	DeleteOnly       bool         // rows have no edit page
	DeleteNote       string       // consequences shown in the delete confirm, English key
	Links            []Option     // extra toolbar buttons: Value = href
	Filters          []Filter     // selects next to the search box
	SortKeys         []string     // parallel to Cols; "" = not sortable
	Sort, Dir        string       // current sort key and "asc"/"desc"
	InvoiceLink      bool         // first cell links to Base/ID/invoice
	NoDelete         bool         // rows have no delete button
	Actions          []RowAction  // per-row POST buttons
	Dates            bool         // from/to date inputs next to the search box
	From, To         string       // YYYY-MM-DD
	Bulk             []BulkAction // row checkboxes + buttons posting the selected ids to Base/Path
	Clean            string       // POST URL of the "keep N days / Clean logs" form; "" = none
	Note, NoteLink   string       // muted line under the title, linking to NoteLink; "" = none
}

// BulkAll reports whether a bulk action acts on all matching rows; the bar then shows without a selection.
func (lp ListPage) BulkAll() bool {
	for _, b := range lp.Bulk {
		if b.All {
			return true
		}
	}
	return false
}

// BulkAction is a button that POSTs the checked row ids (name "ids") to Base/Path.
type BulkAction struct {
	Path, Label string
	Danger      bool
	All         bool // acts on all matching rows, ignores the selection
	Page        bool // the handler shows its own confirm page, so no browser confirm first
}

// ListSort reads ?sort and ?dir against a whitelist. param is "key_dir" for the SQL CASE ("" = default order).
func ListSort(r *http.Request, allowed ...string) (sort, dir, param string) {
	g := r.URL.Query().Get
	dir = "asc"
	if !httpx.OneOf(g("sort"), allowed...) {
		return "", dir, ""
	}
	if g("dir") == "desc" {
		dir = "desc"
	}
	return g("sort"), dir, g("sort") + "_" + dir
}

type Filter struct {
	Name, Val string
	Opts      []Option
	Label     string // translated; the select's accessible name
}

// RowAction is a per-row POST button to Base/ID/Path; Field adds a number input of that name.
type RowAction struct {
	Path, Label, Field string
	Confirm            bool // asks "Label name?" first
}

func (lp ListPage) Query() url.Values {
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
	if lp.Nav.Per != 0 && lp.Nav.Per != view.PerPage {
		v.Set("per", strconv.Itoa(lp.Nav.Per))
	}
	return v
}

// numericCols are right-aligned in lists: amounts, counts and sizes.
var numericCols = map[string]bool{"Balance": true, "Plan Price": true, "Value": true, "Max Usage": true, "Used": true,
	"Min Order": true, "Download": true, "Upload": true, "Port Amount": true, "Attenuation": true}

// secondaryCols are hidden below sm (phones): the first column, status, amounts and the row actions stay.
// Matched by header name, like numericCols; the first column is never hidden (see IsSecondary).
var secondaryCols = map[string]bool{"Full Name": true, "Package": true, "Plan Name": true, "Service Type": true, "PPPoE Username": true,
	"Phone": true, "Type": true, "Options": true, "Required": true, "Order": true, "Max Usage": true, "Min Order": true,
	"Start Date": true, "End Date": true, "Used": true, "Created": true, "Created On": true, "Method": true,
	"Location": true, "Username": true, "Actor": true, "Description": true, "IP": true, "IP / CIDR": true,
	"Address": true, "Coverage": true, "Coordinates": true, "Local IP": true, "IP Range": true, "Router": true,
	"Host": true, "Enabled": true, "Last Seen": true, "Last Login": true, "User Type": true, "Burst": true,
	"Plan Validity": true, "Features": true}

// IsNum reports whether column i is numeric (right-aligned, header included).
func (lp ListPage) IsNum(i int) bool {
	return i < len(lp.Cols) && numericCols[lp.Cols[i]]
}

// IsSecondary reports whether column i is hidden on phones (max-sm:hidden). Column 0 always stays.
func (lp ListPage) IsSecondary(i int) bool {
	return i > 0 && i < len(lp.Cols) && secondaryCols[lp.Cols[i]]
}

// primaryCols stay visible on phones and are emphasised there (the expiry date on subscriptions).
var primaryCols = map[string]bool{"Expires On": true}

// IsPrimary reports whether column i is a primary column on phones.
func (lp ListPage) IsPrimary(i int) bool {
	return i < len(lp.Cols) && primaryCols[lp.Cols[i]]
}

// ColClass is the class list of the header and body cells of column i ("" for plain columns).
// On phones the Status column is hidden: its colour dot sits in the first cell instead (see list.html).
func (lp ListPage) ColClass(i int) string {
	var c []string
	if lp.IsNum(i) {
		c = append(c, "num")
	}
	if lp.IsSecondary(i) {
		c = append(c, "max-sm:hidden")
	}
	if lp.IsPrimary(i) {
		c = append(c, "font-medium")
	}
	if i > 0 && i < len(lp.Cols) && lp.Cols[i] == "Status" {
		c = append(c, "max-sm:hidden")
	}
	return strings.Join(c, " ")
}

// StatusIndex is the column of the Status value, -1 when the list has none.
func (lp ListPage) StatusIndex() int {
	for i, c := range lp.Cols {
		if c == "Status" {
			return i
		}
	}
	return -1
}

// FilterCount is the number of filters in use: dates and selects (the search box is always visible).
func (lp ListPage) FilterCount() int {
	n := 0
	if lp.From != "" {
		n++
	}
	if lp.To != "" {
		n++
	}
	for _, f := range lp.Filters {
		if f.Val != "" {
			n++
		}
	}
	return n
}

// rowItem is one control in a row's action cell: an edit link, a delete form or a POST action form.
type rowItem struct {
	Kind    string // "edit", "delete" or "action"
	Href    string // link target or form action
	Label   string
	Icon    string
	Field   string // number input name, "" = none
	Name    string // first cell of the row, used in confirm texts
	Confirm bool   // asks first
	Danger  bool
	Note    string // delete: consequences, appended to the confirm text
	Primary bool   // the one control shown on phones when the row has more than two
	Busy    string // action: label shown on the button while its request runs (data-busy-label)
}

// actionBusy is the busy label of a row action, "" when the action has none.
func actionBusy(path string) string {
	if path == "test" {
		return "Contacting router…"
	}
	return ""
}

// Tags splits a cell of a comma-separated list (the Features column) into its badges.
func (lp ListPage) Tags(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ", ")
}

// actionIcon is the icon of a row action by its path.
func actionIcon(path string) string {
	switch path {
	case "extend":
		return "calendar-plus"
	case "deactivate", "toggle":
		return "power"
	case "sync":
		return "refresh-cw"
	case "test":
		return "wifi"
	case "up":
		return "arrow-up"
	case "down":
		return "arrow-down"
	}
	return "circle-check"
}

// RowItems lists the controls of a row in desktop order: actions, edit, delete. Primary is the edit link,
// or the first control when there is none.
func (lp ListPage) RowItems(r ListRow) []rowItem {
	id := strconv.FormatInt(r.ID, 10)
	name := ""
	if len(r.Cells) > 0 {
		name = r.Cells[0]
	}
	var items []rowItem
	for _, a := range lp.Actions {
		items = append(items, rowItem{Kind: "action", Href: lp.Base + "/" + id + "/" + a.Path,
			Label: a.Label, Icon: actionIcon(a.Path), Field: a.Field, Name: name, Confirm: a.Confirm, Danger: a.Path == "deactivate", Busy: actionBusy(a.Path)})
	}
	if !lp.DeleteOnly {
		items = append(items, rowItem{Kind: "edit", Href: lp.Base + "/" + id + "/edit", Label: "Edit", Icon: "pencil", Name: name})
	}
	if !lp.NoDelete {
		items = append(items, rowItem{Kind: "delete", Href: lp.Base + "/" + id + "/delete", Label: "Delete", Icon: "trash-2", Name: name, Confirm: true, Danger: true, Note: lp.DeleteNote})
	}
	if len(items) > 0 {
		p := 0
		for i, it := range items {
			if it.Kind == "edit" {
				p = i
			}
		}
		items[p].Primary = true
	}
	return items
}

// Filtered reports whether a search, date, Filter or sort is applied; the Reset control shows then.
func (lp ListPage) Filtered() bool {
	if lp.Q != "" || lp.From != "" || lp.To != "" || lp.Sort != "" {
		return true
	}
	for _, f := range lp.Filters {
		if f.Val != "" {
			return true
		}
	}
	return false
}

// SortKey is the sort key of column i, "" when it is not sortable.
func (lp ListPage) SortKey(i int) string {
	if i < len(lp.SortKeys) {
		return lp.SortKeys[i]
	}
	return ""
}

// SortURL is the header link for a column: the same sort flips direction.
func (lp ListPage) SortURL(key string) string {
	v := lp.Query()
	v.Set("sort", key)
	v.Set("dir", "asc")
	if lp.Sort == key && lp.Dir == "asc" {
		v.Set("dir", "desc")
	}
	return lp.Base + "?" + v.Encode()
}

// finish drops the extra row and sets the view.Pager from the count of all matching rows.
func (lp *ListPage) Finish(pg view.Pager, total int64) {
	more := len(lp.Rows) > pg.Per
	if more {
		lp.Rows = lp.Rows[:pg.Per]
	}
	lp.Nav = view.NewPageNav(pg, total, len(lp.Rows), more, func(page, per int) string {
		return view.PageHref(lp.Base, lp.Query(), page, per)
	})
}

func (s *Base) RenderList(w http.ResponseWriter, r *http.Request, lp ListPage) {
	s.Render(w, r, http.StatusOK, "list", core.Page{
		Title: lp.Heading,
		Flash: s.Sessions.PopString(r.Context(), "flash"),
		Error: s.Sessions.PopString(r.Context(), "error"),
		Data:  lp,
	})
}
