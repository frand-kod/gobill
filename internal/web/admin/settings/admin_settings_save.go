package settings

// Saving a Settings tab; the transaction is in service/settingsave.

import (
	"log/slog"
	"net/http"

	"github.com/frand-kod/gobill/internal/service/settingsave"
	"github.com/frand-kod/gobill/internal/web/admin/adminx"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"github.com/frand-kod/gobill/internal/web/view"

	"errors"
	"fmt"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
)

func (s *Handlers) settingSaver() *settingsave.Service {
	return &settingsave.Service{DB: s.Conn, Q: s.Queries}
}

func (s *Handlers) settingsSave(w http.ResponseWriter, r *http.Request) {
	tab, ok := s.settingsTab(w, r)
	if !ok {
		return
	}
	fields := forRole(s.SettingsFields(tab, nil, nil, nil), r)
	keys := adminx.FieldNames(fields)
	// the body limit allows every image field of the tab at its 2 MB cap (each file is checked on its
	// own when saved); only a bigger body fails the parse, shown on the file fields
	files := int64(1)
	for _, k := range keys {
		if settingsFile[k] {
			files++
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, files*adminx.MaxUpload+64<<10)
	if err := r.ParseMultipartForm(adminx.MaxUpload); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		e := map[string]string{}
		for _, k := range keys {
			if settingsFile[k] || k == "qris_payload" {
				e[k] = "File must be 2 MB or less"
			}
		}
		s.renderSettings(w, r, http.StatusUnprocessableEntity, tab, map[string]string{}, e)
		return
	}
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "load settings", err)
		return
	}
	v := httpx.FormVals(r, keys...)
	for _, f := range fields {
		if f.Type == "checkbox" { // stored as yes/no, the PHP convention every reader expects
			v[f.Name] = map[bool]string{true: "yes", false: "no"}[v[f.Name] == "1"]
		}
		if f.Type == "file" { // the stored image stays unless "remove" is ticked or a new one is uploaded
			v[f.Name] = st[f.Name]
			if r.PostFormValue(f.Name+"_remove") == "1" {
				v[f.Name] = ""
			}
		}
	}
	if r.PostFormValue("qris_payload_remove") == "1" {
		v["qris_payload"] = ""
	}
	qerr := adminx.QrisUpload(r, v)
	errs := s.SettingsErrors(v)
	if qerr != "" {
		errs["qris_payload"] = qerr
	}
	if len(errs) > 0 {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, tab, v, errs)
		return
	}
	if tab == "wireguard" {
		n, rewrite, off, err := s.net.WGImpact(r.Context(), st, v)
		if err != nil {
			s.Fail(w, "count wireguard peers", err)
			return
		}
		if n > 0 && (rewrite || off) && v["wg_confirm"] != "yes" {
			msg := s.T("%d routers have a tunnel and will be cut. Tick the box to confirm")
			s.renderSettings(w, r, http.StatusUnprocessableEntity, tab, v, map[string]string{"wg_confirm": fmt.Sprintf(msg, n)})
			return
		}
	}
	e := map[string]string{}
	for _, k := range keys {
		if !settingsFile[k] || r.MultipartForm == nil || len(r.MultipartForm.File[k]) == 0 {
			continue
		}
		name, err := s.StoreUpload(r.MultipartForm.File[k][0])
		if err != nil {
			e[k] = err.Error()
			continue
		}
		v[k] = name
	}
	if len(e) > 0 {
		s.renderSettings(w, r, http.StatusUnprocessableEntity, tab, v, e)
		return
	}

	var rewrite bool
	if tab == "wireguard" {
		var err error
		if _, rewrite, _, err = s.net.WGImpact(r.Context(), st, v); err != nil {
			s.Fail(w, "count wireguard peers", err)
			return
		}
	}
	if err := s.settingSaver().Save(r.Context(), settingsave.Input{Tab: tab, Keys: keys, V: v, WgRewrite: rewrite}); err != nil {
		s.Fail(w, "save settings", err)
		return
	}
	var replaced []string // images no longer in use: replaced or removed
	for _, k := range keys {
		if settingsFile[k] && st[k] != "" && st[k] != v[k] {
			replaced = append(replaced, st[k])
		}
	}
	s.RemoveUnused(r.Context(), replaced)

	if tab == "isolir" && st["isolir_enabled"] == "yes" && v["isolir_enabled"] != "yes" && s.Billing != nil {
		// switching isolation off ends the running ones; the settings stay saved if this fails
		if err := s.Billing.EndIsolations(r.Context()); err != nil {
			slog.Error("isolir: end isolations after switching off", "err", err)
		}
	}
	if tab == "wireguard" && s.WG != nil {
		s.WG.Reload(r.Context()) // an error shows in the tab's status
	}
	if l, ok := v["language"]; ok {
		s.Lang.Store(l)
	}
	if x, ok := v["thousands_sep"]; ok {
		view.SetThousandsSep(x)
	}
	s.SettingsSaved(r.Context())
	s.Done(w, r, "/admin/settings/"+tab, "Settings saved", "settings.save", tab)
}

// notifReset is POST /admin/settings/notifications/reset/{name}: the message goes back to its built-in text.
func (s *Handlers) notifReset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	def := notify.DefaultTemplate(name)
	if def == "" {
		http.NotFound(w, r)
		return
	}
	if err := s.Queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: "notif_" + name, Value: def}); err != nil {
		s.Fail(w, "reset notif "+name, err)
		return
	}
	s.SettingsSaved(r.Context())
	s.Done(w, r, "/admin/settings/notifications", "Settings saved", "settings.reset", "notif "+name)
}
