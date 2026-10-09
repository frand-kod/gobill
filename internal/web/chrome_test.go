package web

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// Flash messages render once, inside the fixed toast container, before the page flow.
func TestFlashInToastContainer(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	do(h, "POST", "/admin/settings/localisation", url.Values{"language": {"english"}, "timezone": {"Asia/Jakarta"},
		"date_format": {"d-m-Y"}, "country_code_phone": {"62"}}, c)
	body := do(h, "GET", "/admin/settings/localisation", nil, c).Body.String()
	box, main := strings.Index(body, `id="toasts"`), strings.Index(body, "<main")
	if box < 0 || main < 0 || box > main {
		t.Fatalf("toast container missing or inside the page flow (toasts %d, main %d)", box, main)
	}
	if i := strings.Index(body, "Settings saved"); i < box || i > main {
		t.Fatal("success flash not inside the toast container")
	}
	if strings.Count(body, "Settings saved") != 1 {
		t.Fatal("flash rendered more than once")
	}
	if !strings.Contains(body[box:main], `role="status" data-toast="4000"`) {
		t.Fatal("success toast needs role=status and a 4s timer")
	}
}

func TestToastRolesAndTimers(t *testing.T) {
	s, _ := newTestApp(t)
	var b bytes.Buffer
	p := Page{Title: "Test", Admin: &db.Admin{Role: "SuperAdmin", Username: "alice"}, Brand: s.brand(t.Context()),
		Flash: "saved-msg", Warn: "warn-msg", Error: "error-msg", Detail: "sql detail"}
	if err := s.templates["list"].ExecuteTemplate(&b, "base", p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`role="status" data-toast="4000"`, `saved-msg`,
		`role="status" data-toast="8000"`, `warn-msg`,
		`role="alert">`, `error-msg`, `sql detail`,
		`aria-label="Tutup"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("toast markup missing %q", want)
		}
	}
	if strings.Contains(out, `role="alert" data-toast`) {
		t.Error("errors must not auto-dismiss")
	}
}

// The intro is closed by default and is a popover anchored to the "?" button.
func TestIntroClosedPopover(t *testing.T) {
	h, _ := newTestServer(t)
	c := login(t, h, "alice")
	body := do(h, "GET", "/admin/customers", nil, c).Body.String()
	if !strings.Contains(body, `<details class="intro-help relative" data-intro>`) {
		t.Fatal("intro popover missing")
	}
	if strings.Contains(body, "data-intro open") || strings.Contains(body, `class="intro-help relative" data-intro open`) {
		t.Fatal("intro open by default")
	}
	for _, want := range []string{`aria-controls="intro-pop"`, `id="intro-pop" class="intro-pop" role="dialog"`, `data-intro-close`} {
		if !strings.Contains(body, want) {
			t.Errorf("intro markup missing %q", want)
		}
	}
	if strings.Contains(body, "gb-hide") {
		t.Error("per-page intro dismissal is gone")
	}
}

// The footer shows the company name (settings, fallback gobill) and the build version.
func TestFooterVersionAndCompany(t *testing.T) {
	s, _ := newTestApp(t)
	s.Version = "1.2.3"
	h := s.Handler()
	c := login(t, h, "alice")
	body := do(h, "POST", "/admin/settings/app", settingsForm("Acme ISP"), c)
	if body.Code != 303 {
		t.Fatalf("settings save: %d", body.Code)
	}
	page := do(h, "GET", "/admin", nil, c).Body.String()
	for _, want := range []string{"<footer", "Acme ISP", "v1.2.3", "GPL-3.0", "https://github.com/frand-kod/gobill", "/admin/docs"} {
		if !strings.Contains(page, want) {
			t.Errorf("admin footer missing %q", want)
		}
	}
	if !strings.Contains(page, " - Acme ISP</title>") {
		t.Error("title does not use the company name")
	}
}

func TestNoHardcodedBrandName(t *testing.T) {
	h, _ := newTestServer(t)
	pages := []string{"/login", "/portal/login"}
	c := login(t, h, "alice")
	pages = append(pages, "/admin", "/admin/customers", "/admin/settings/app")
	for _, p := range pages {
		cookie := c
		if p == "/login" || p == "/portal/login" {
			cookie = nil
		}
		w := do(h, "GET", p, nil, cookie)
		if w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
		if strings.Contains(w.Body.String(), "NuxBill") {
			t.Errorf("%s still shows the NuxBill brand", p)
		}
	}
}

func settingsForm(company string) url.Values {
	return url.Values{"company_name": {company}, "company_footer": {"Acme ISP"}, "address": {"Jl. Merdeka 1"},
		"phone": {"0812"}, "note": {"Thanks"}, "currency_code": {"Rp"}}
}
