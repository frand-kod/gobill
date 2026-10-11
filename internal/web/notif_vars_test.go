package web

import (
	"net/http"
	"strings"
	"testing"

	gobill "github.com/frand-kod/gobill"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/i18n"
	"github.com/frand-kod/gobill/internal/notify"
)

// Each message template lists its variables as chips, and has a reset button when it has a built-in text.
func TestNotifTemplatesShowVariablesAndReset(t *testing.T) {
	_, h, _ := settingsSetup(t)
	c := login(t, h, "alice")
	body := getBody(t, h, c, "/admin/settings/notifications")
	for _, v := range notify.Vars["expired"] {
		if !strings.Contains(body, `data-var-insert="[[`+v.Name+`]]"`) {
			t.Errorf("expired: chip for [[%s]] missing", v.Name)
		}
	}
	if !strings.Contains(body, `formaction="/admin/settings/notifications/reset/expired"`) {
		t.Error("no reset button for the expired message")
	}
	if !strings.Contains(body, `data-var-insert="[[balance_before]]"`) || !strings.Contains(body, `formaction="/admin/settings/notifications/reset/invoice_balance"`) {
		t.Error("invoice_balance lacks its variables or its reset button")
	}
	if !strings.Contains(body, `src="/static/tplvars.js"`) {
		t.Error("the chip script is not loaded")
	}
	if !strings.Contains(getBody(t, h, c, "/admin/settings/isolir"), `data-var-insert="[[qris_image]]"`) {
		t.Error("the Isolir custom HTML has no variable chips")
	}
}

// Reset puts the built-in text back, from the server; an unknown name is a 404 and a report role gets none.
func TestNotifResetRestoresDefault(t *testing.T) {
	_, h, q := settingsSetup(t)
	c := login(t, h, "alice")
	if err := q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "notif_expired", Value: "my own text"}); err != nil {
		t.Fatal(err)
	}
	if w := do(h, "POST", "/admin/settings/notifications/reset/expired", nil, c); w.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d", w.Code)
	}
	if got := settingValues(t, q)["notif_expired"]; got != notify.DefaultTemplate("expired") {
		t.Fatalf("after reset: %q", got)
	}
	if w := do(h, "POST", "/admin/settings/notifications/reset/nope", nil, c); w.Code != http.StatusNotFound {
		t.Fatalf("unknown template: %d", w.Code)
	}
	if w := do(h, "POST", "/admin/settings/notifications/reset/expired", nil, login(t, h, "rita")); w.Code != http.StatusForbidden {
		t.Fatalf("report role: %d", w.Code)
	}
}

// Every variable description is translated, so the chips read in Indonesian too.
func TestVarDescriptionsTranslated(t *testing.T) {
	cat, err := i18n.Load(gobill.FS, "lang")
	if err != nil {
		t.Fatal(err)
	}
	id := cat["indonesia"]
	var all []notify.Var
	for _, vs := range notify.Vars {
		all = append(all, vs...)
	}
	all = append(all, isolirVarList...)
	for _, v := range all {
		if id[v.Desc] == "" {
			t.Errorf("%s: %q has no Indonesian text", v.Name, v.Desc)
		}
	}
}
