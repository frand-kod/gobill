package view

// Paging: the ?page and ?per request and the Pager under a table.

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// PerPage is the default rows per page; ?per= may pick any of PageSizes.
const PerPage = 20

var PageSizes = []int{20, 50, 100}

// Pager is the page request: ?page (1-based) and ?per (one of PageSizes).
type Pager struct{ Page, Per int }

// Limit is one row more than a page, so finish can tell if a next page exists.
func (pg Pager) Limit() int64 { return int64(pg.Per) + 1 }

func (pg Pager) Offset() int64 { return int64(pg.Page-1) * int64(pg.Per) }

// Paging reads ?q, ?page and ?per. A ?per that is not in PageSizes is PerPage.
func Paging(r *http.Request) (q string, pg Pager) {
	g := r.URL.Query()
	q = strings.TrimSpace(g.Get("q"))
	pg.Page, _ = strconv.Atoi(g.Get("page"))
	if pg.Page < 1 {
		pg.Page = 1
	}
	pg.Per, _ = strconv.Atoi(g.Get("per"))
	if !slices.Contains(PageSizes, pg.Per) {
		pg.Per = PerPage
	}
	return q, pg
}

// PageNav is the Pager under a table: the rows shown, the page-size links and Prev/Next.
type PageNav struct {
	Total, From, To int64
	Per             int
	Prev, Next      string // URLs, "" = none
	Sizes           []PageSize
}

// PageSize is one page-size link of the Pager; On marks the size in use.
type PageSize struct {
	N    int
	Href string
	On   bool
}

// NewPageNav builds the Pager for a page of shown rows. more says a next page exists and href builds the URL of
// a page at a page size, keeping the filters.
func NewPageNav(pg Pager, total int64, shown int, more bool, href func(page, per int) string) PageNav {
	nav := PageNav{Total: total, Per: pg.Per}
	if shown > 0 {
		nav.From = pg.Offset() + 1
		nav.To = nav.From + int64(shown) - 1
	}
	if pg.Page > 1 {
		nav.Prev = href(pg.Page-1, pg.Per)
	}
	if more {
		nav.Next = href(pg.Page+1, pg.Per)
	}
	for _, n := range PageSizes {
		nav.Sizes = append(nav.Sizes, PageSize{N: n, Href: href(1, n), On: n == pg.Per})
	}
	return nav
}

// PageHref is the URL of a page: v plus page, and per when it is not the default page size.
func PageHref(base string, v url.Values, page, per int) string {
	v.Set("page", strconv.Itoa(page))
	if per != PerPage {
		v.Set("per", strconv.Itoa(per))
	} else {
		v.Del("per")
	}
	return base + "?" + v.Encode()
}
