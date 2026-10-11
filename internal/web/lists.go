package web

// List pages: sorting, paging, filters, row and bulk actions, list rendering.

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// perPage is the default rows per page; ?per= may pick any of pageSizes.
const perPage = 20

var pageSizes = []int{20, 50, 100}

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
	Nav              pageNav      // row count, page-size links and Prev/Next under the table
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
	Page        bool // the handler shows its own confirm page, so no browser confirm first
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
	if lp.Nav.Per != 0 && lp.Nav.Per != perPage {
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
	"Plan Validity": true}

// IsNum reports whether column i is numeric (right-aligned, header included).
func (lp listPage) IsNum(i int) bool {
	return i < len(lp.Cols) && numericCols[lp.Cols[i]]
}

// IsSecondary reports whether column i is hidden on phones (max-sm:hidden). Column 0 always stays.
func (lp listPage) IsSecondary(i int) bool {
	return i > 0 && i < len(lp.Cols) && secondaryCols[lp.Cols[i]]
}

// primaryCols stay visible on phones and are emphasised there (the expiry date on subscriptions).
var primaryCols = map[string]bool{"Expires On": true}

// IsPrimary reports whether column i is a primary column on phones.
func (lp listPage) IsPrimary(i int) bool {
	return i < len(lp.Cols) && primaryCols[lp.Cols[i]]
}

// ColClass is the class list of the header and body cells of column i ("" for plain columns).
// On phones the Status column is hidden: its colour dot sits in the first cell instead (see list.html).
func (lp listPage) ColClass(i int) string {
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
func (lp listPage) StatusIndex() int {
	for i, c := range lp.Cols {
		if c == "Status" {
			return i
		}
	}
	return -1
}

// FilterCount is the number of filters in use: dates and selects (the search box is always visible).
func (lp listPage) FilterCount() int {
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

// badgeDot is the colour of a status dot for a badge class from badge().
func badgeDot(badgeClass string) string {
	switch badgeClass {
	case "badge-ok":
		return "bg-ok-fg"
	case "badge-bad":
		return "bg-err-fg"
	case "badge-warn":
		return "bg-warn-fg"
	}
	return "bg-ink-2"
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
	Primary bool // the one control shown on phones when the row has more than two
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
func (lp listPage) RowItems(r listRow) []rowItem {
	id := strconv.FormatInt(r.ID, 10)
	name := ""
	if len(r.Cells) > 0 {
		name = r.Cells[0]
	}
	var items []rowItem
	for _, a := range lp.Actions {
		items = append(items, rowItem{Kind: "action", Href: lp.Base + "/" + id + "/" + a.Path,
			Label: a.Label, Icon: actionIcon(a.Path), Field: a.Field, Name: name, Confirm: a.Confirm, Danger: a.Path == "deactivate"})
	}
	if !lp.DeleteOnly {
		items = append(items, rowItem{Kind: "edit", Href: lp.Base + "/" + id + "/edit", Label: "Edit", Icon: "pencil", Name: name})
	}
	if !lp.NoDelete {
		items = append(items, rowItem{Kind: "delete", Href: lp.Base + "/" + id + "/delete", Label: "Delete", Icon: "trash-2", Name: name, Confirm: true, Danger: true})
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

// Filtered reports whether a search, date, filter or sort is applied; the Reset control shows then.
func (lp listPage) Filtered() bool {
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
func (lp listPage) SortKey(i int) string {
	if i < len(lp.SortKeys) {
		return lp.SortKeys[i]
	}
	return ""
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

// pager is the page request: ?page (1-based) and ?per (one of pageSizes).
type pager struct{ Page, Per int }

// limit is one row more than a page, so finish can tell if a next page exists.
func (pg pager) limit() int64 { return int64(pg.Per) + 1 }

func (pg pager) offset() int64 { return int64(pg.Page-1) * int64(pg.Per) }

// paging reads ?q, ?page and ?per. A ?per that is not in pageSizes is perPage.
func paging(r *http.Request) (q string, pg pager) {
	g := r.URL.Query()
	q = strings.TrimSpace(g.Get("q"))
	pg.Page, _ = strconv.Atoi(g.Get("page"))
	if pg.Page < 1 {
		pg.Page = 1
	}
	pg.Per, _ = strconv.Atoi(g.Get("per"))
	if !slices.Contains(pageSizes, pg.Per) {
		pg.Per = perPage
	}
	return q, pg
}

// pageNav is the pager under a table: the rows shown, the page-size links and Prev/Next.
type pageNav struct {
	Total, From, To int64
	Per             int
	Prev, Next      string // URLs, "" = none
	Sizes           []pageSize
}

// pageSize is one page-size link of the pager; On marks the size in use.
type pageSize struct {
	N    int
	Href string
	On   bool
}

// newPageNav builds the pager for a page of shown rows. more says a next page exists and href builds the URL of
// a page at a page size, keeping the filters.
func newPageNav(pg pager, total int64, shown int, more bool, href func(page, per int) string) pageNav {
	nav := pageNav{Total: total, Per: pg.Per}
	if shown > 0 {
		nav.From = pg.offset() + 1
		nav.To = nav.From + int64(shown) - 1
	}
	if pg.Page > 1 {
		nav.Prev = href(pg.Page-1, pg.Per)
	}
	if more {
		nav.Next = href(pg.Page+1, pg.Per)
	}
	for _, n := range pageSizes {
		nav.Sizes = append(nav.Sizes, pageSize{N: n, Href: href(1, n), On: n == pg.Per})
	}
	return nav
}

// pageHref is the URL of a page: v plus page, and per when it is not the default page size.
func pageHref(base string, v url.Values, page, per int) string {
	v.Set("page", strconv.Itoa(page))
	if per != perPage {
		v.Set("per", strconv.Itoa(per))
	} else {
		v.Del("per")
	}
	return base + "?" + v.Encode()
}

// finish drops the extra row and sets the pager from the count of all matching rows.
func (lp *listPage) finish(pg pager, total int64) {
	more := len(lp.Rows) > pg.Per
	if more {
		lp.Rows = lp.Rows[:pg.Per]
	}
	lp.Nav = newPageNav(pg, total, len(lp.Rows), more, func(page, per int) string {
		return pageHref(lp.Base, lp.query(), page, per)
	})
}

func (s *Server) renderList(w http.ResponseWriter, r *http.Request, lp listPage) {
	s.render(w, r, http.StatusOK, "list", Page{
		Title: lp.Heading,
		Flash: s.sessions.PopString(r.Context(), "flash"),
		Error: s.sessions.PopString(r.Context(), "error"),
		Data:  lp,
	})
}
