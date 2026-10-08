package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestMaintenanceMode(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	cust, _ := custLogin(t, e, "u1", "pw12345")
	if cust == nil {
		t.Fatal("customer login failed")
	}
	set := func(k, v string) {
		if err := e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatal(err)
		}
	}

	set("maintenance_mode", "yes")
	for _, path := range []string{"/portal/login", "/portal/register"} {
		if w := do(e.h, "GET", path, nil, nil); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "<h1>") {
			t.Errorf("GET %s during maintenance: %d", path, w.Code)
		}
	}
	if w := do(e.h, "GET", "/portal", nil, cust); w.Code != http.StatusServiceUnavailable {
		t.Errorf("portal with customer session: %d, want 503", w.Code)
	}
	if w := do(e.h, "GET", "/login", nil, nil); w.Code != 200 {
		t.Errorf("/login during maintenance: %d", w.Code)
	}
	if w := do(e.h, "GET", "/admin/customers", nil, e.c); w.Code != 200 {
		t.Errorf("admin during maintenance: %d", w.Code)
	}
	if w := do(e.h, "POST", "/portal/login", url.Values{"username": {"u1"}, "password": {"pw12345"}}, nil); w.Code != http.StatusServiceUnavailable {
		t.Errorf("portal login POST during maintenance: %d", w.Code)
	}

	// Customer sessions survive while the logout option is off.
	set("maintenance_mode", "no")
	if w := do(e.h, "GET", "/portal", nil, cust); w.Code != 200 {
		t.Errorf("portal after maintenance: %d", w.Code)
	}

	// With the logout option on, the customer is signed out during maintenance.
	set("maintenance_mode", "yes")
	set("maintenance_mode_logout", "yes")
	if w := do(e.h, "GET", "/portal", nil, cust); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("portal with logout mode: %d", w.Code)
	}
	set("maintenance_mode", "no")
	if w := do(e.h, "GET", "/portal", nil, cust); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/portal/login" {
		t.Errorf("customer should be logged out, got %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestMaintenanceAllowsCallback(t *testing.T) {
	e := payApp(t)
	e.q.UpsertSetting(t.Context(), db.UpsertSettingParams{Key: "maintenance_mode", Value: "yes"})
	if w := postCallback(e.h, `{}`, "x"); w.Code == http.StatusServiceUnavailable {
		t.Fatal("callback blocked by maintenance")
	}
}
