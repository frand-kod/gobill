package web

// Settings page: form, render and validation hook.

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/settings"
	"github.com/frand-kod/gobill/internal/web/core"

	"fmt"
	"slices"
	"strconv"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/job"
	"github.com/frand-kod/gobill/internal/wg"
)

// settingsErrors checks the posted values of one sub-page, see settings.Validate.
func (s *Server) settingsErrors(v map[string]string) map[string]string {
	return settings.Validate(v, s.Catalog.Languages())
}

// settingsTab reads the sub-page from the path. It 404s an unknown page and 403s a role that may not open it.
func (s *Server) settingsTab(w http.ResponseWriter, r *http.Request) (string, bool) {
	tab := r.PathValue("tab")
	for _, t := range settingsTabs {
		if t.Slug == "wireguard" && !wg.Built {
			continue // the nowg build has no such tab
		}
		if t.Slug == tab {
			if t.SuperOnly && adminFrom(r).Role != "SuperAdmin" {
				s.deny(w, r, http.StatusForbidden)
				return "", false
			}
			return tab, true
		}
	}
	s.deny(w, r, http.StatusNotFound)
	return "", false
}

func (s *Server) settingsForm(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("tab") == "" {
		http.Redirect(w, r, "/admin/settings/app", http.StatusSeeOther)
		return
	}
	tab, ok := s.settingsTab(w, r)
	if !ok {
		return
	}
	values, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "load settings", err)
		return
	}
	if values["wa_url"] == "" { // legacy sms_url: shown in the gateway field until the page is saved
		values["wa_url"] = values["sms_url"]
	}
	s.renderSettings(w, r, http.StatusOK, tab, values, nil)
}

// settingsNav is the sub-page menu for the caller's role (SuperOnly tabs only for SuperAdmin).
func settingsNav(r *http.Request) []core.Option {
	var nav []core.Option
	for _, t := range settingsTabs {
		if (!t.SuperOnly || adminFrom(r).Role == "SuperAdmin") && (t.Slug != "wireguard" || wg.Built) {
			nav = append(nav, core.Option{Value: "/admin/settings/" + t.Slug, Label: t.Label})
		}
	}
	return nav
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, status int, tab string, v, e map[string]string) {
	nav := settingsNav(r)
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "load settings", err)
		return
	}
	// a field absent from v (e.g. after a body too big to parse) shows the stored value
	for k, x := range st {
		if _, ok := v[k]; !ok {
			v[k] = x
		}
	}
	if v["log_keep_days"] == "" { // never set: show the value the cleanup job uses
		v["log_keep_days"] = strconv.Itoa(billing.LogKeepDefault)
	}
	if v["backup_keep"] == "" {
		v["backup_keep"] = strconv.Itoa(job.BackupKeepDefault)
	}
	if v["expired_notify_minutes_before"] == "" { // never set: show the value the sender uses
		v["expired_notify_minutes_before"] = strconv.Itoa(billing.ExpiredNoticeDefault)
	}
	if tab == "isolir" { // never saved = the default shown
		for k := range isolir.Defaults {
			if _, ok := v[k]; !ok {
				v[k] = isolir.Value(st, k)
			}
		}
		name, down, up := s.isolirSpeed(r.Context())
		if name == "" {
			name = "Not created yet. Created when Isolation is enabled"
		}
		v["isolir_plan_name"] = name
		if _, ok := v["isolir_rate_down"]; !ok { // a posted rate is kept on a failed save
			v["isolir_rate_down"], v["isolir_rate_up"] = down, up
		}
	}
	if tab == "wireguard" { // never saved = the default shown
		for k, x := range wgDefaults(st) {
			if v[k] == "" && k != "wg_endpoint" {
				v[k] = x
			}
		}
	}
	fp := formPage{Heading: "Settings", Action: "/admin/settings/" + tab, Cancel: "/admin", Fields: forRole(s.settingsFields(tab, v, e, st), r)}
	if tab == "isolir" {
		fp.Fields = append(fp.Fields, s.isolirSetupFields(st)...)
	}
	if tab == "wireguard" {
		n, _ := s.Queries.CountWgPeers(r.Context())
		for i, f := range fp.Fields {
			if f.Name == "wg_confirm" {
				fp.Fields[i].Hint = fmt.Sprintf(s.T("%d routers have a tunnel. Changing the port, endpoint or subnet cuts them until they are provisioned again, and switching WireGuard off cuts them too"), n)
			}
		}
		fp.Fields = append(fp.Fields, s.wgStatusFields(r.Context(), st)...)
	}
	if tab == "payment" { // the URL to paste into the Tripay merchant dashboard
		fp.Fields[0].Hint = "Callback URL for Tripay: " + core.BaseURL(r) + "/callback/tripay" + ", Duitku: " + core.BaseURL(r) + "/callback/duitku"
	}
	if tok := s.Sessions.PopString(r.Context(), "metrics_token_once"); tok != "" && tab == "integrations" {
		snippet := "scrape_configs:\n  - job_name: gobill\n    metrics_path: /metrics\n    authorization:\n      type: Bearer\n      credentials: " + tok +
			"\n    static_configs:\n      - targets: ['" + r.Host + "']"
		once := field{Name: "metrics_token_once", Label: "New /metrics token", Type: "tokenshow", Value: tok, Snippet: snippet,
			Section: "New token", Hint: "Copy it now. It is not shown again"}
		fp.Fields = append([]field{once}, fp.Fields...)
	}
	var warn string
	if tab == "system" && adminFrom(r).Role == "SuperAdmin" { // backup is SuperAdmin only, as in the old dbstatus page
		links := []field{{Name: "backup", Label: "Database backup", Type: "link", Value: "/admin/settings/system/backup", Section: "System", Hint: s.backupStatus(st)},
			{Name: "key", Label: "Encryption key", Type: "link", Value: keyPage, Btn: "Open key page",
				Hint: fmt.Sprintf(s.T("Fingerprint: %s. Keep a copy outside the backup mirror."), keyFingerprint(s.SecretKey)), Section: "System"},
			{Name: "import", Label: "Import PHPNuxBill", Type: "link", Value: importPage, Btn: "Open import page",
				Hint: "Replaces the data in this app. A backup is made first.", Section: "System"},
			{Name: "restore", Label: "Restore database", Type: "link", Value: restorePage, Btn: "Open restore page",
				Hint: "Replaces the data with a gobill backup and restarts the app. A backup is made first.", Section: "System"}}
		// the links join the System card, which comes before the Hotspot card
		at := len(fp.Fields)
		for i, f := range fp.Fields {
			if f.Section == "System" {
				at = i + 1
			}
		}
		fp.Fields = slices.Insert(fp.Fields, at, links...)
		warn = s.keyWarning(r)
	}
	s.Render(w, r, status, "form", Page{Title: "Settings", Flash: s.Sessions.PopString(r.Context(), "flash"), Error: s.Sessions.PopString(r.Context(), "error"), Tabs: nav, Data: fp, Warn: warn})
}
