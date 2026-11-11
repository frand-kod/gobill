package web

// Quick Setup: step list, saved state and progress.

import (
	"context"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
)

// Settings keys the wizard keeps. setupKey is "" (not started), "started", "dismissed" (Lewati) or "done".
const (
	setupKey      = "setup_wizard"
	setupModeKey  = "setup_mode"   // "new", or "import" once a PHPNuxBill import finished
	setupRouter   = "setup_router" // "radius", "api" or "both"
	setupVoucher  = "setup_test_voucher"
	importPageURL = "/admin/settings/system/import"
)

// wizStep is one page of the wizard. Titles and texts are catalog keys (lang/indonesia.json).
type wizStep struct {
	Slug, Title, Why, Learn, Docs string
	Optional                      bool
}

var wizSteps = []wizStep{
	{"welcome", "Welcome", "gobill is ready in a few minutes. Start from scratch, or move your customers, plans and payments from PHPNuxBill.",
		"Starting from scratch fills the basic settings step by step. Importing copies the PHPNuxBill database; every step then shows what was imported so you can check it.", "quick-setup", false},
	{"company", "Business and app URL", "Your business name and phone appear on invoices and on the customer portal. The app URL is the address customers and routers use to reach gobill.",
		"The app URL is filled from the address you used to log in. Change it when gobill is reached through a domain or another port.", "configuration", false},
	{"router", "Router or NAS", "gobill has to talk to your MikroTik. RADIUS (recommended) lets the router ask gobill who may log in. The API lets gobill read and control the router directly.",
		"After saving the NAS, paste the commands into the MikroTik terminal. The indicator turns green when the router sends its first RADIUS packet.", "mikrotik", false},
	{"bandwidth", "Speed", "A speed is the download and upload limit a plan uses. Three common speeds are prepared; change them, or leave a field empty to skip it.",
		"Speeds are saved as bandwidth rows. A speed with the same name is kept, not created twice.", "configuration", false},
	{"plan", "Plans", "A plan is what the customer buys: a price, a validity and a speed. Daily, weekly and monthly templates are prepared with suggested prices.",
		"Plans stay editable in the Plans page. With RADIUS they are served by the RADIUS server; with the API they belong to the router you choose.", "configuration", false},
	{"whatsapp", "WhatsApp", "gobill sends invoices, reminders and expiry notices through WhatsApp using a GOWA gateway. Enter the gateway address, then send a test message.",
		"Without a gateway no message is sent, but everything else keeps working.", "integrations", false},
	{"qris", "QRIS payment code", "The static QRIS of your merchant lets customers pay by scanning. Upload the QRIS image; gobill reads the code in it. Scan the preview to check it.",
		"Only the code text is stored, not the image.", "integrations", false},
	{"tripay", "Tripay (optional)", "Tripay is a payment gateway that creates a payment for each invoice and confirms it automatically. Skip it if QRIS is enough.",
		"The keys come from your Tripay dashboard. Sandbox is for testing.", "integrations", true},
	{"isolir", "Isolation (optional)", "Isolation sends customers whose plan has expired to a page that asks them to pay, instead of cutting them off. It needs a few commands on the MikroTik.",
		"Turn it on, then paste the commands into the router. Details are in the guide.", "isolation", true},
	{"customer", "First customer or voucher", "Make one test voucher for a plan. You use it in the last step to check that a customer can log in. Real customers can be added later.",
		"A voucher is a prepaid code the customer types on the hotspot login page.", "configuration", false},
	{"finish", "Finish", "Last check: log in on the hotspot with the test voucher. When the router reports the session, the indicator below turns green.",
		"Finishing hides Quick setup from the menu. Every setting stays editable on its normal page.", "README", false},
}

// wizSpeeds are the speed presets in Mbps; wizPlans the plan templates of the Plans step.
var wizSpeeds = []string{"5", "10", "20"}

var wizPlans = []struct {
	Name, Unit, Label string
	Validity, Price   int64
}{
	{"Harian", "Days", "Daily price (Rp)", 1, 5000},
	{"Mingguan", "Days", "Weekly price (Rp)", 7, 25000},
	{"Bulanan", "Months", "Monthly price (Rp)", 1, 100000},
}

const (
	wizSave = iota
	wizTest
)

func wizFind(slug string) (wizStep, int, bool) {
	for i, st := range wizSteps {
		if st.Slug == slug {
			return st, i, true
		}
	}
	return wizStep{}, 0, false
}

// wizHref is the page of step i: "" before the first step, /admin after the last.
func wizHref(i int) string {
	switch {
	case i < 0:
		return ""
	case i >= len(wizSteps):
		return "/admin"
	}
	return "/admin/setup/" + wizSteps[i].Slug
}

// wizState is what the steps are judged by: row counts, settings and the chosen router mode.
type wizState struct {
	cnt    db.CountSetupRow
	st     map[string]string // loadSettings, defaults included
	raw    map[string]string // stored settings only
	mode   string            // setupModeKey
	router string            // setupRouter
}

func (s *Server) wizLoad(ctx context.Context) (wizState, error) {
	var ws wizState
	var err error
	if ws.cnt, err = s.queries.CountSetup(ctx); err != nil {
		return ws, err
	}
	if ws.st, err = s.loadSettings(ctx); err != nil {
		return ws, err
	}
	if ws.raw, err = s.Settings.All(ctx); err != nil {
		return ws, err
	}
	ws.mode, ws.router = ws.raw[setupModeKey], ws.raw[setupRouter]
	return ws, nil
}

// effectiveRouter is the chosen router mode, or the one the existing rows imply when none is chosen.
func (ws wizState) effectiveRouter() string {
	switch {
	case ws.router != "":
		return ws.router
	case ws.cnt.Nas > 0:
		return "radius"
	case ws.cnt.Routers > 0:
		return "api"
	}
	return ""
}

// done reports whether the rows and settings of a step exist.
func (ws wizState) done(slug string) bool {
	c := ws.cnt
	switch slug {
	case "welcome":
		return ws.mode != "" || c.Routers+c.Nas+c.Bandwidths+c.Plans+c.Customers+c.Vouchers > 0
	case "company":
		return ws.raw["company_name"] != "" && ws.raw["phone"] != ""
	case "router":
		switch ws.effectiveRouter() {
		case "api":
			return c.Routers > 0
		case "both":
			return c.Routers > 0 && c.Nas > 0
		case "radius":
			return c.Nas > 0
		}
		return false
	case "bandwidth":
		return c.Bandwidths > 0
	case "plan":
		return c.Plans > 0
	case "whatsapp":
		return notify.WAConfigured(ws.st)
	case "qris":
		return ws.st["qris_payload"] != ""
	case "tripay":
		return ws.st["payment_gateway"] == "tripay"
	case "isolir":
		return ws.st["isolir_enabled"] == "yes"
	case "customer":
		return c.Customers+c.Vouchers > 0
	case "finish":
		return ws.raw[setupKey] == "done"
	}
	return false
}

// progress counts the required steps (the optional ones and the finish page do not count).
func (ws wizState) progress() (done, total int) {
	for _, st := range wizSteps {
		if st.Optional || st.Slug == "finish" {
			continue
		}
		total++
		if ws.done(st.Slug) {
			done++
		}
	}
	return
}

// setupNav is what the layout shows a SuperAdmin: the sidebar item, the first-run dialog and the progress badge.
type setupNav struct {
	Show     bool // sidebar item: not finished and not dismissed
	Modal    bool // first page load: offer the wizard
	Done     int
	Total    int
	Finished bool // done or dismissed: the Guide links to the wizard
}

// setupNavFor works out the layout state. An install that already has every required step predates
// the wizard, so it is marked done instead of being offered the wizard.
func (s *Server) setupNavFor(ctx context.Context, path string) (*setupNav, error) {
	ws, err := s.wizLoad(ctx)
	if err != nil {
		return nil, err
	}
	state := ws.raw[setupKey]
	done, total := ws.progress()
	if state == "" && total > 0 && done == total {
		if err := s.queries.UpsertSetting(ctx, db.UpsertSettingParams{Key: setupKey, Value: "done"}); err != nil {
			return nil, err
		}
		state = "done"
	}
	return &setupNav{Show: state == "" || state == "started", Modal: state == "" && !strings.HasPrefix(path, "/admin/setup"),
		Done: done, Total: total, Finished: state == "done" || state == "dismissed"}, nil
}
