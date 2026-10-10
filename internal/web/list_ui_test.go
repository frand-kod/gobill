package web

import (
	"strings"
	"testing"
)

// The reset control is the only icon link with this href and class, so it is matched without the (translated) label.
func TestListResetOnlyWhenFiltered(t *testing.T) {
	_, h, q, c := crudApp(t)
	reset := func(page, base string) bool {
		return strings.Contains(do(h, "GET", page, nil, c).Body.String(), `btn btn-ghost btn-icon tip-end" href="`+base+`"`)
	}
	if reset("/admin/vouchers", "/admin/vouchers") || reset("/admin/subscriptions", "/admin/subscriptions") {
		t.Fatal("reset shown without a filter")
	}
	if !reset("/admin/vouchers?q=abc", "/admin/vouchers") || !reset("/admin/subscriptions?sort=expires&dir=asc", "/admin/subscriptions") {
		t.Fatal("no reset with a search or sort")
	}
	newCust(t, q, "bulk1", "Bulk One", "0899", "", "Active")
	if !reset("/admin/customers?service_type=PPPoE", "/admin/customers") || reset("/admin/customers", "/admin/customers") {
		t.Fatal("customers: reset not tied to the filter")
	}
}

// The selection bar is in the markup for bulk lists, and CSS keeps it hidden until a row is checked.
func TestBulkBarMarkup(t *testing.T) {
	_, h, q, c := crudApp(t)
	newCust(t, q, "bulk1", "Bulk One", "0899", "", "Active")
	body := do(h, "GET", "/admin/customers", nil, c).Body.String()
	for _, want := range []string{`<form id="bulk" method="post" class="bulk-bar">`, "data-bulk-count", `data-select-all`, `form="bulk"`} {
		if !strings.Contains(body, want) {
			t.Errorf("customers list lacks %q", want)
		}
	}
	if !strings.Contains(body, `formaction="/admin/customers/message"`) {
		t.Error("customers bulk button not in the selection bar")
	}
	css := do(h, "GET", "/static/app.css", nil, nil).Body.String()
	if !strings.Contains(css, ".bulk-bar{display:none}") {
		t.Error("app.css does not hide the selection bar by default")
	}
}

// Sorted headers carry the active arrow, and numeric columns are right-aligned.
func TestListSortIndicator(t *testing.T) {
	_, h, _, c := crudApp(t)
	body := do(h, "GET", "/admin/customers?sort=balance&dir=desc", nil, c).Body.String()
	if !strings.Contains(body, `sort-ic sort-on`) || !strings.Contains(body, `aria-sort="descending"`) {
		t.Error("active sort arrow missing")
	}
	if !strings.Contains(body, `<th class="num">`) {
		t.Error("Balance header not right-aligned")
	}
}
