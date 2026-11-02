package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A 422 with field errors shows a focused summary at the top that links to each field; the field and its hint
// are tied together with aria-describedby, and a field with an error is never hidden by its x-show condition.
func TestFormErrorSummaryAndForcedVisible(t *testing.T) {
	e := billApp(t)
	f := planPost("Bad", url.Values{"bandwidth_id": {itoa(e.bw)}, "type": {"PPPoE"}, "device": {""},
		"router_id": {itoa(e.rt)}, "limited": {"1"}, "limit_type": {"Data_Limit"}, "data_limit": {"5"}})
	w := do(e.h, "POST", "/admin/plans", f, e.c)
	wantCode(t, w, http.StatusUnprocessableEntity, "PPPoE data limit on the MikroTik API")
	b := w.Body.String()
	for _, want := range []string{`id="form-errors"`, `role="alert"`, `tabindex="-1"`, `kolom perlu diperbaiki`, `href="#f-limit_type"`, `limit_type-err"`} {
		if !strings.Contains(b, want) {
			i := strings.Index(b, "f-limit_type")
			t.Fatalf("missing %q in %.900s", want, b[i-50:])
		}
	}
	i := strings.Index(b, `id="f-limit_type"`)
	if i < 0 {
		t.Fatal("limit_type field missing")
	}
	tag := b[i:]
	tag = tag[:strings.Index(tag, ">")]
	if strings.Contains(tag, "x-show") || strings.Contains(tag, "x-effect") {
		t.Fatalf("field with an error is still conditional: %s", tag)
	}
}

// A field hidden by its condition is not submitted: the wrapper disables its inputs while the condition is false.
func TestHiddenFieldsGated(t *testing.T) {
	b := planForm(t, billApp(t))
	if !strings.Contains(b, `x-effect="$el.querySelectorAll('input,select,textarea')`) || !strings.Contains(b, `e.disabled=!(`) {
		i := strings.Index(b, "f-time_limit")
		t.Fatalf("not disabled: %.600s", b[i:])
	}
}

func TestListFilteredEmpty(t *testing.T) {
	e := billApp(t)
	b := do(e.h, "GET", "/admin/customers?q=zzzznomatch", nil, e.c).Body.String()
	if !strings.Contains(b, "No results for this filter") && !strings.Contains(b, "Tidak ada hasil untuk filter ini") {
		t.Fatal("filtered empty list should say there is no result for the filter")
	}
	if strings.Contains(b, "Nothing here yet") || strings.Contains(b, "Belum ada data") {
		t.Fatal("filtered empty list must not say there is no data")
	}
}

func TestStyledNotFound(t *testing.T) {
	e := billApp(t)
	w := do(e.h, "GET", "/admin/no-such-page", nil, e.c)
	wantCode(t, w, http.StatusNotFound, "unknown path")
	b := w.Body.String()
	for _, want := range []string{`<html lang="`, "gb-theme", `href="/static/`} {
		if !strings.Contains(b, want) {
			t.Fatalf("404 page missing %q", want)
		}
	}
	w = do(e.h, "GET", "/static/no-such.css", nil, e.c)
	if strings.Contains(w.Body.String(), "<html") {
		t.Fatal("static 404 should stay plain")
	}
}
