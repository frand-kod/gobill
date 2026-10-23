package web

// Quick Setup ("Quick setup"): a SuperAdmin wizard with one page per install step.
// The wizard owns no data. Each save replays the existing handler of that feature (NAS, router,
// bandwidth, plan) or writes the settings keys the Settings pages write, so validation and side
// effects stay in one place. Progress is read back from the rows: a step is done when its data exists.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/skip2/go-qrcode"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/payment"
)

// Settings keys the wizard keeps. setupKey is "" (not started), "started", "dismissed" (Lewati) or "done".
const (
	setupKey      = "setup_wizard"
	setupModeKey  = "setup_mode"   // "new", or "import" once a PHPNuxBill import finished
	setupRouter   = "setup_router" // "radius", "api" or "both"
	setupVoucher  = "setup_test_voucher"
	importPageURL = "/admin/settings/miscellaneous/import"
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
	rows, err := s.queries.ListSettings(ctx)
	if err != nil {
		return ws, err
	}
	ws.raw = make(map[string]string, len(rows))
	for _, r := range rows {
		ws.raw[r.Key] = r.Value
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

// setupRoutes registers the wizard. Everything is SuperAdmin only.
func (s *Server) setupRoutes(mux *http.ServeMux, sa func(http.Handler) http.Handler) {
	mux.Handle("GET /admin/setup", sa(http.HandlerFunc(s.setupIndex)))
	mux.Handle("GET /admin/setup/live.json", sa(http.HandlerFunc(s.setupLive)))
	mux.Handle("POST /admin/setup/start", sa(http.HandlerFunc(s.setupStart)))
	mux.Handle("POST /admin/setup/dismiss", sa(http.HandlerFunc(s.setupDismiss)))
	mux.Handle("POST /admin/setup/finish", sa(http.HandlerFunc(s.setupFinish)))
	mux.Handle("GET /admin/setup/{step}", sa(http.HandlerFunc(s.setupShow)))
	mux.Handle("POST /admin/setup/{step}", sa(s.setupAct(wizSave)))
	mux.Handle("POST /admin/setup/{step}/test", sa(s.setupAct(wizTest)))
	mux.Handle("POST /admin/setup/{step}/skip", sa(http.HandlerFunc(s.setupSkip)))
}

// setupIndex opens the first step that is not done yet.
func (s *Server) setupIndex(w http.ResponseWriter, r *http.Request) {
	ws, err := s.wizLoad(r.Context())
	if err != nil {
		s.fail(w, "setup", err)
		return
	}
	dest := wizHref(len(wizSteps) - 1)
	for i, st := range wizSteps {
		if !st.Optional && st.Slug != "finish" && !ws.done(st.Slug) {
			dest = wizHref(i)
			break
		}
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// setupShow renders one step page.
func (s *Server) setupShow(w http.ResponseWriter, r *http.Request) {
	st, _, ok := wizFind(r.PathValue("step"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ws, err := s.wizLoad(r.Context())
	if err != nil {
		s.fail(w, "setup", err)
		return
	}
	view, err := s.wizPage(r, st, ws, nil, nil, "")
	if err != nil {
		s.fail(w, "setup "+st.Slug, err)
		return
	}
	s.render(w, r, http.StatusOK, "setup", Page{Title: "Quick setup", Data: view, Warn: s.proxyWarning(r)})
}

// setupStart answers the first-run dialog. to is "new" (Mulai baru), "import" or "" (Mulai setup cepat).
func (s *Server) setupStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, _ := s.queries.GetSetting(ctx, setupKey)
	if cur == "" {
		if err := s.queries.UpsertSetting(ctx, db.UpsertSettingParams{Key: setupKey, Value: "started"}); err != nil {
			s.fail(w, "setup start", err)
			return
		}
	}
	dest := wizHref(0)
	switch r.PostFormValue("to") {
	case "import":
		dest = importPageURL
	case "new":
		if err := s.queries.UpsertSetting(ctx, db.UpsertSettingParams{Key: setupModeKey, Value: "new"}); err != nil {
			s.fail(w, "setup start", err)
			return
		}
		dest = wizHref(1)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// setupDismiss is Lewati: the dialog never comes back.
func (s *Server) setupDismiss(w http.ResponseWriter, r *http.Request) {
	if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: setupKey, Value: "dismissed"}); err != nil {
		s.fail(w, "setup dismiss", err)
		return
	}
	s.flashTo(w, r, "/admin", "Quick setup hidden. Open it again from the Guide if you need it.")
}

// setupFinish is "Ke Dashboard" on the last page.
func (s *Server) setupFinish(w http.ResponseWriter, r *http.Request) {
	if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: setupKey, Value: "done"}); err != nil {
		s.fail(w, "setup finish", err)
		return
	}
	s.flashTo(w, r, "/admin", "Quick setup finished. Every setting stays editable on its page.")
}

// setupSkip is Lewati langkah: go on without saving anything.
func (s *Server) setupSkip(w http.ResponseWriter, r *http.Request) {
	_, idx, ok := wizFind(r.PathValue("step"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, wizHref(idx+1), http.StatusSeeOther)
}

// setupLive is the JSON the live indicators poll: nas_seen once the NAS sent a RADIUS packet (metric
// radius_nas_last_packet_timestamp, read as status.go does), voucher_online while a session of the voucher runs.
func (s *Server) setupLive(w http.ResponseWriter, r *http.Request) {
	out := map[string]bool{}
	q := r.URL.Query()
	if ip := strings.TrimSpace(q.Get("nas")); ip != "" {
		out["nas_seen"] = metrics.Value("radius_nas_last_packet_timestamp", "nas", ip) > 0
	}
	if code := strings.TrimSpace(q.Get("voucher")); code != "" {
		var n int64
		if err := s.conn.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM radius_sessions WHERE username = ? AND stopped_at IS NULL", code).Scan(&n); err != nil {
			s.fail(w, "setup live", err)
			return
		}
		out["voucher_online"] = n > 0
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

// wizResult is what a save produced: next means saved, go on; otherwise the page is shown again with v and e.
type wizResult struct {
	next   bool
	v, e   map[string]string
	secret string // NAS secret saved just now, shown in the RouterOS commands once
}

// setupAct runs the save (wizSave) or the test (wizTest) of a step.
func (s *Server) setupAct(kind int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, idx, ok := wizFind(r.PathValue("step"))
		if !ok || st.Slug == "finish" {
			http.NotFound(w, r)
			return
		}
		ctx := r.Context()
		ws, err := s.wizLoad(ctx)
		if err != nil {
			s.fail(w, "setup", err)
			return
		}
		res, err := s.wizSave(r, st, ws, kind)
		if err != nil {
			s.fail(w, "setup "+st.Slug, err)
			return
		}
		if res.next {
			s.sessions.Put(ctx, "flash", s.catalog.T(s.language(), "Saved"))
			http.Redirect(w, r, wizHref(idx+1), http.StatusSeeOther)
			return
		}
		if ws, err = s.wizLoad(ctx); err != nil {
			s.fail(w, "setup", err)
			return
		}
		view, err := s.wizPage(r, st, ws, res.v, res.e, res.secret)
		if err != nil {
			s.fail(w, "setup "+st.Slug, err)
			return
		}
		status, page := http.StatusOK, Page{Title: "Quick setup", Data: view}
		if len(res.e) > 0 {
			status = http.StatusUnprocessableEntity
			page.Error = s.catalog.T(s.language(), "Please check the highlighted fields")
			if m := res.e[""]; m != "" {
				page.Error = m
			}
		}
		s.render(w, r, status, "setup", page)
	}
}

// wizSave runs the save of one step.
func (s *Server) wizSave(r *http.Request, st wizStep, ws wizState, kind int) (wizResult, error) {
	switch st.Slug {
	case "company":
		return s.wizSaveCompany(r)
	case "router":
		return s.wizSaveRouter(r, kind)
	case "bandwidth":
		return s.wizSaveSpeeds(r)
	case "plan":
		return s.wizSavePlans(r, ws)
	case "whatsapp":
		return s.wizSaveWA(r, kind)
	case "qris":
		return s.wizSaveQRIS(r, ws)
	case "tripay":
		return s.wizSaveTripay(r)
	case "isolir":
		return s.wizSaveIsolir(r)
	case "customer":
		return s.wizSaveVoucher(r)
	}
	return wizResult{next: true}, nil // welcome: nothing to save
}

// wizPut writes settings the way the Settings pages do: one upsert per key.
func (s *Server) wizPut(ctx context.Context, kv map[string]string) error {
	for k, v := range kv {
		if err := s.queries.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			return err
		}
	}
	if s.SettingsChanged != nil {
		s.SettingsChanged(ctx)
	}
	return nil
}

func (s *Server) wizSaveCompany(r *http.Request) (wizResult, error) {
	v := formVals(r, "company_name", "app_url", "phone")
	if e := s.settingsErrors(v); len(e) > 0 {
		return wizResult{v: v, e: e}, nil
	}
	if err := s.wizPut(r.Context(), v); err != nil {
		return wizResult{}, err
	}
	return wizResult{next: true}, nil
}

// wizSaveRouter saves the NAS (RADIUS) and/or the router (API). An existing row with the same IP or
// host is edited, never duplicated. The step stays open so the commands and the live indicator show.
func (s *Server) wizSaveRouter(r *http.Request, kind int) (wizResult, error) {
	ctx := r.Context()
	v := formVals(r, "router_mode", "nas_name", "nas_ip", "nas_secret", "router_name", "router_host", "router_port", "router_username", "router_password")
	e := map[string]string{}
	res := wizResult{v: v, e: e}
	mode := v["router_mode"]
	if !oneOf(mode, "radius", "api", "both") {
		e["router_mode"] = "Invalid value"
		return res, nil
	}
	if mode != "api" {
		id, err := s.wizNASByIP(ctx, v["nas_ip"])
		if err != nil {
			return wizResult{}, err
		}
		form := url.Values{"name": {v["nas_name"]}, "ip": {v["nas_ip"]}, "secret": {v["nas_secret"]}}
		if id != 0 { // keep what the NAS form does not show
			n, err := s.queries.GetNAS(ctx, id)
			if err != nil {
				return wizResult{}, err
			}
			form.Set("description", n.Description)
			if n.RequireMessageAuth != 0 {
				form.Set("require_message_auth", "1")
			}
		}
		if ok, errs := s.wizReplay(r, http.HandlerFunc(s.nasSave), id, form); !ok {
			wizMerge(e, "nas_", errs)
		} else if v["nas_secret"] != "" {
			res.secret = v["nas_secret"]
		}
	}
	if mode != "radius" && len(e) == 0 {
		id, err := s.wizRouterByHost(ctx, v["router_host"])
		if err != nil {
			return wizResult{}, err
		}
		form := url.Values{"name": {v["router_name"]}, "host": {v["router_host"]}, "port": {v["router_port"]},
			"username": {v["router_username"]}, "password": {v["router_password"]}, "enabled": {"1"}}
		if id != 0 {
			x, err := s.queries.GetRouter(ctx, id)
			if err != nil {
				return wizResult{}, err
			}
			form.Set("description", x.Description)
			form.Set("coordinates", x.Coordinates)
			form.Set("coverage", strconv.FormatInt(x.Coverage, 10))
			if x.Enabled == 1 {
				form.Set("enabled", "1")
			} else {
				form.Del("enabled")
			}
		}
		if ok, errs := s.wizReplay(r, http.HandlerFunc(s.routerSave), id, form); !ok {
			wizMerge(e, "router_", errs)
		} else if kind == wizTest {
			s.wizPing(r, v["router_host"])
		}
	}
	if len(e) > 0 {
		return res, nil
	}
	if err := s.wizPut(ctx, map[string]string{setupRouter: mode}); err != nil {
		return wizResult{}, err
	}
	s.sessions.Put(ctx, "flash", s.catalog.T(s.language(), "Saved"))
	return res, nil
}

// wizPing runs the Routers page connection test on a saved router and leaves the answer as a flash.
func (s *Server) wizPing(r *http.Request, host string) {
	ctx, lang := r.Context(), s.language()
	id, err := s.wizRouterByHost(ctx, host)
	if err != nil || id == 0 {
		return
	}
	x, err := s.queries.GetRouter(ctx, id)
	if err != nil {
		return
	}
	switch {
	case s.Billing == nil:
		s.sessions.Put(ctx, "error", s.catalog.T(lang, "Router connection is not configured"))
	default:
		if ident, err := s.Billing.Ping(ctx, x); err != nil {
			s.sessions.Put(ctx, "error", s.catalog.T(lang, "Connection failed")+": "+x.Name+". "+s.catalog.T(lang, routerErr(err, msgRouterOther)))
		} else {
			s.sessions.Put(ctx, "flash", s.catalog.T(lang, "Connection successful")+": "+x.Name+" ("+ident+")")
		}
	}
}

// wizSaveSpeeds creates the speed presets that are filled in and not yet there.
func (s *Server) wizSaveSpeeds(r *http.Request) (wizResult, error) {
	ctx := r.Context()
	v := map[string]string{}
	e := map[string]string{}
	have := map[string]bool{}
	rows, err := s.queries.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 1000})
	if err != nil {
		return wizResult{}, err
	}
	for _, b := range rows {
		have[b.Name] = true
	}
	for i := range wizSpeeds {
		key := speedKey(i)
		rate := strings.TrimSpace(r.PostFormValue(key))
		v[key] = rate
		if rate == "" {
			continue
		}
		if _, ok := posInt(rate); !ok {
			e[key] = "Enter a number greater than 0"
			continue
		}
		name := rate + " Mbps"
		if have[name] {
			continue
		}
		ok, errs := s.wizReplay(r, http.HandlerFunc(s.bwSave), 0, url.Values{"name": {name}, "rate_down": {rate}, "rate_down_unit": {"Mbps"},
			"rate_up": {rate}, "rate_up_unit": {"Mbps"}, "burst": {""}})
		if !ok {
			e[key] = joinErrs(errs)
		}
	}
	if len(e) > 0 {
		return wizResult{v: v, e: e}, nil
	}
	return wizResult{next: true}, nil
}

// wizSavePlans creates the plan templates that have a price and no plan of that name yet.
// The plans follow the router mode: RADIUS plans have no router, API plans belong to the chosen router.
func (s *Server) wizSavePlans(r *http.Request, ws wizState) (wizResult, error) {
	ctx := r.Context()
	v := formVals(r, "plan_bandwidth", "plan_router")
	e := map[string]string{}
	for i := range wizPlans {
		v[priceKey(i)] = strings.TrimSpace(r.PostFormValue(priceKey(i)))
	}
	have := map[string]bool{}
	rows, err := s.queries.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
	if err != nil {
		return wizResult{}, err
	}
	for _, p := range rows {
		have[p.Name] = true
	}
	api := ws.effectiveRouter() == "api"
	for i, p := range wizPlans {
		key := priceKey(i)
		if v[key] == "" || have[p.Name] {
			continue
		}
		form := url.Values{"name": {p.Name}, "type": {"Hotspot"}, "billing": {"prepaid"}, "price": {v[key]},
			"validity": {strconv.FormatInt(p.Validity, 10)}, "validity_unit": {p.Unit}, "bandwidth_id": {v["plan_bandwidth"]},
			"enabled": {"1"}, "audience": {"public"}, "device": {"Radius"}}
		if api {
			form.Set("device", "MikrotikHotspot")
			form.Set("router_id", v["plan_router"])
		}
		if ok, errs := s.wizReplay(r, http.HandlerFunc(s.planSave), 0, form); !ok {
			for k, msg := range errs {
				switch k {
				case "bandwidth_id":
					e["plan_bandwidth"] = msg
				case "router_id":
					e["plan_router"] = msg
				default:
					e[key] = msg
				}
			}
		}
	}
	if len(e) > 0 {
		return wizResult{v: v, e: e}, nil
	}
	return wizResult{next: true}, nil
}

// wizSaveWA saves the gateway fields, or sends the test message through the same code as Integrations.
func (s *Server) wizSaveWA(r *http.Request, kind int) (wizResult, error) {
	if kind == wizTest {
		v, e, err := s.waTestSend(r)
		if err != nil {
			return wizResult{}, err
		}
		return wizResult{v: v, e: e}, nil
	}
	kv := map[string]string{}
	for _, k := range []string{"alt_wga_server_url", "alt_wga_device_id", "alt_wga_username"} {
		kv[k] = strings.TrimSpace(r.PostFormValue(k))
	}
	if pw := r.PostFormValue("alt_wga_password"); pw != "" { // empty keeps the saved password
		kv["alt_wga_password"] = pw
	}
	if x := r.PostFormValue("notify_customers"); x == "yes" || x == "no" {
		kv["notify_customers"] = x
	}
	if err := s.wizPut(r.Context(), kv); err != nil {
		return wizResult{}, err
	}
	return wizResult{next: true}, nil
}

// wizSaveQRIS stores the QRIS text read from an uploaded image (the same decoder as Settings > Payment).
func (s *Server) wizSaveQRIS(r *http.Request, ws wizState) (wizResult, error) {
	_ = r.ParseMultipartForm(maxUpload) // a plain POST has no files; the form values still parse
	current := ws.st["qris_payload"]
	text := strings.TrimSpace(r.PostFormValue("qris_payload"))
	if r.PostFormValue("qris_payload_remove") == "1" {
		text = ""
	}
	v := map[string]string{"qris_payload": text}
	e := map[string]string{}
	if qerr := qrisUpload(r, v); qerr != "" {
		e["qris_payload"] = qerr
	} else if v["qris_payload"] != "" && payment.QRISValid(v["qris_payload"]) != nil {
		e["qris_payload"] = "Paste the full static QRIS text, the CRC must match"
	}
	if len(e) > 0 {
		return wizResult{v: v, e: e}, nil
	}
	if v["qris_payload"] != current {
		if err := s.wizPut(r.Context(), map[string]string{"qris_payload": v["qris_payload"]}); err != nil {
			return wizResult{}, err
		}
	}
	return wizResult{next: true}, nil
}

func (s *Server) wizSaveTripay(r *http.Request) (wizResult, error) {
	kv := map[string]string{"payment_gateway": ""}
	if r.PostFormValue("tripay_on") == "1" {
		kv["payment_gateway"] = "tripay"
	}
	for _, k := range []string{"tripay_merchant_code", "tripay_mode", "tripay_channel"} {
		kv[k] = strings.TrimSpace(r.PostFormValue(k))
	}
	if kv["tripay_mode"] == "" {
		kv["tripay_mode"] = "sandbox"
	}
	for _, k := range []string{"tripay_api_key", "tripay_private_key"} { // empty keeps the saved secret
		if x := r.PostFormValue(k); x != "" {
			kv[k] = x
		}
	}
	if e := s.settingsErrors(kv); len(e) > 0 {
		return wizResult{v: kv, e: e}, nil
	}
	if err := s.wizPut(r.Context(), kv); err != nil {
		return wizResult{}, err
	}
	return wizResult{next: true}, nil
}

// wizSaveIsolir turns Isolir on or off. The isolir plan is created the first time, as the Settings page does.
func (s *Server) wizSaveIsolir(r *http.Request) (wizResult, error) {
	ctx := r.Context()
	kv := map[string]string{"isolir_enabled": "no"}
	if r.PostFormValue("isolir_on") == "1" {
		kv["isolir_enabled"] = "yes"
		id, err := isolir.EnsurePlan(ctx, s.queries)
		if err != nil {
			return wizResult{}, err
		}
		kv["isolir_plan_id"] = strconv.FormatInt(id, 10)
	}
	if err := s.wizPut(ctx, kv); err != nil {
		return wizResult{}, err
	}
	return wizResult{next: true}, nil
}

// wizSaveVoucher makes the one test voucher, unless the saved one still exists.
func (s *Server) wizSaveVoucher(r *http.Request) (wizResult, error) {
	ctx := r.Context()
	v := formVals(r, "voucher_plan")
	e := map[string]string{}
	planID, ok := posInt(v["voucher_plan"])
	if !ok {
		e["voucher_plan"] = "This field is required"
		return wizResult{v: v, e: e}, nil
	}
	if _, err := s.queries.GetPlan(ctx, planID); err != nil {
		if err == sql.ErrNoRows {
			e["voucher_plan"] = "Invalid value"
			return wizResult{v: v, e: e}, nil
		}
		return wizResult{}, err
	}
	if cur, _ := s.queries.GetSetting(ctx, setupVoucher); cur != "" {
		if _, err := s.queries.GetVoucherByCode(ctx, cur); err == nil {
			return wizResult{next: true}, nil
		}
	}
	if err := s.newVoucher(r, planID, "", upperChars, minVoucherLen); err != nil {
		return wizResult{}, err
	}
	last, err := s.queries.ListVouchers(ctx, db.ListVouchersParams{Limit: 1})
	if err != nil {
		return wizResult{}, err
	}
	if len(last) == 0 {
		return wizResult{}, sql.ErrNoRows
	}
	if err := s.wizPut(ctx, map[string]string{setupVoucher: last[0].Code}); err != nil {
		return wizResult{}, err
	}
	return wizResult{next: true}, nil
}

// wizNASByIP is the id of the NAS with that address, 0 when there is none.
func (s *Server) wizNASByIP(ctx context.Context, ip string) (int64, error) {
	rows, err := s.queries.ListNAS(ctx)
	if err != nil {
		return 0, err
	}
	for _, n := range rows {
		if n.Ip == ip {
			return n.ID, nil
		}
	}
	return 0, nil
}

// wizRouterByHost is the id of the router with that host, 0 when there is none.
func (s *Server) wizRouterByHost(ctx context.Context, host string) (int64, error) {
	rows, err := s.queries.ListRouters(ctx, db.ListRoutersParams{Limit: 1000})
	if err != nil {
		return 0, err
	}
	for _, x := range rows {
		if x.Host == host {
			return x.ID, nil
		}
	}
	return 0, nil
}

// wizRecorder keeps what a save handler writes, so the wizard can read the redirect or the rejected form.
type wizRecorder struct {
	hdr  http.Header
	code int
	body bytes.Buffer
}

func (w *wizRecorder) Header() http.Header { return w.hdr }

func (w *wizRecorder) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

func (w *wizRecorder) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.body.Write(b)
}

// ponytail: field errors are scraped from the handler's rendered HTML (formfields.html "-err" ids); split
// the NAS/router/bandwidth/plan save handlers into validate+save funcs if this markup changes.
var wizFieldErr = regexp.MustCompile(`id="([a-z_]+)-err">([^<]*)<`)

// wizReplay runs an existing save handler on the posted values, the way its own form would post them.
// It returns ok for a saved row; otherwise the field errors the handler rendered, keyed by its field names.
func (s *Server) wizReplay(r *http.Request, h http.HandlerFunc, id int64, form url.Values) (bool, map[string]string) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, r.URL.Path, strings.NewReader(form.Encode()))
	if err != nil {
		return false, map[string]string{"": err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if id != 0 {
		req.SetPathValue("id", strconv.FormatInt(id, 10))
	}
	rec := &wizRecorder{hdr: http.Header{}}
	h(rec, req)
	if rec.code == http.StatusSeeOther {
		return true, nil
	}
	errs := map[string]string{}
	for _, m := range wizFieldErr.FindAllStringSubmatch(rec.body.String(), -1) {
		errs[m[1]] = html.UnescapeString(m[2])
	}
	if len(errs) == 0 {
		errs[""] = "Could not save this step"
	}
	return false, errs
}

// wizMerge copies the errors of a rejected save into the step's errors, under the step's field names.
func wizMerge(e map[string]string, prefix string, errs map[string]string) {
	for k, msg := range errs {
		if k == "" {
			e[""] = msg
			continue
		}
		e[prefix+k] = msg
	}
}

func joinErrs(errs map[string]string) string {
	msgs := make([]string, 0, len(errs))
	for _, m := range errs {
		msgs = append(msgs, m)
	}
	sort.Strings(msgs)
	return strings.Join(msgs, ". ")
}

func wizDataURI(png []byte) template.URL {
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
}

func speedKey(i int) string { return "speed_" + strconv.Itoa(i) }
func priceKey(i int) string { return "price_" + strconv.Itoa(i) }

// radiusCommands are the RouterOS lines for a RADIUS NAS: the gobill address, the shared secret,
// CoA, and interim updates for hotspot and PPP.
func radiusCommands(host, ip, secret string) []string {
	if secret == "" {
		secret = "<secret>"
	}
	return []string{
		"/radius add service=hotspot,ppp address=" + host + " secret=" + secret + " authentication-port=1812 accounting-port=1813 timeout=1s",
		"/radius incoming set accept=yes",
		"/ip hotspot profile set [find default=yes] use-radius=yes radius-interim-update=1m",
		"/ppp aaa set use-radius=yes interim-update=1m",
	}
}

// wizDefaults are the values a step starts with: the saved data, or the suggestions.
func (s *Server) wizDefaults(r *http.Request, slug string, ws wizState) (map[string]string, error) {
	ctx := r.Context()
	st := ws.st
	v := map[string]string{}
	switch slug {
	case "company":
		v["company_name"], v["app_url"], v["phone"] = st["company_name"], st["app_url"], st["phone"]
	case "router":
		v["router_mode"] = ws.effectiveRouter()
		if v["router_mode"] == "" {
			v["router_mode"] = "radius"
		}
		nas, err := s.queries.ListNAS(ctx)
		if err != nil {
			return nil, err
		}
		if len(nas) > 0 {
			v["nas_name"], v["nas_ip"] = nas[0].Name, nas[0].Ip
		}
		routers, err := s.queries.ListRouters(ctx, db.ListRoutersParams{Limit: 1000})
		if err != nil {
			return nil, err
		}
		v["router_port"], v["router_username"] = "8728", "admin"
		if len(routers) > 0 {
			x := routers[0]
			v["router_name"], v["router_host"], v["router_port"], v["router_username"] = x.Name, x.Host, strconv.FormatInt(x.Port, 10), x.Username
		}
	case "bandwidth":
		for i, sp := range wizSpeeds {
			v[speedKey(i)] = sp
		}
	case "plan":
		bws, err := s.queries.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 1000})
		if err != nil {
			return nil, err
		}
		for _, b := range bws {
			if v["plan_bandwidth"] == "" || b.Name == "10 Mbps" {
				v["plan_bandwidth"] = strconv.FormatInt(b.ID, 10)
			}
		}
		routers, err := s.queries.ListRouters(ctx, db.ListRoutersParams{Limit: 1000})
		if err != nil {
			return nil, err
		}
		if len(routers) > 0 {
			v["plan_router"] = strconv.FormatInt(routers[0].ID, 10)
		}
		plans, err := s.queries.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
		if err != nil {
			return nil, err
		}
		for i, p := range wizPlans {
			v[priceKey(i)] = strconv.FormatInt(p.Price, 10)
			for _, x := range plans {
				if x.Name == p.Name {
					v[priceKey(i)] = strconv.FormatInt(x.Price, 10)
				}
			}
		}
	case "whatsapp":
		for _, k := range []string{"alt_wga_server_url", "alt_wga_device_id", "alt_wga_username"} {
			v[k] = st[k]
		}
		v["notify_customers"] = st["notify_customers"]
		if v["notify_customers"] == "" {
			v["notify_customers"] = "yes"
		}
	case "qris":
		v["qris_payload"] = st["qris_payload"]
	case "tripay":
		v["tripay_merchant_code"], v["tripay_mode"], v["tripay_channel"] = st["tripay_merchant_code"], st["tripay_mode"], st["tripay_channel"]
		if st["payment_gateway"] == "tripay" {
			v["tripay_on"] = "1"
		}
		if v["tripay_mode"] == "" {
			v["tripay_mode"] = "sandbox"
		}
	case "isolir":
		if st["isolir_enabled"] == "yes" {
			v["isolir_on"] = "1"
		}
	case "customer":
		plans, err := s.queries.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
		if err != nil {
			return nil, err
		}
		if len(plans) > 0 {
			v["voucher_plan"] = strconv.FormatInt(plans[0].ID, 10)
		}
	}
	return v, nil
}

// wizView builds the page of one step. v and e are the posted values and errors, or nil for a fresh page.
func (s *Server) wizPage(r *http.Request, st wizStep, ws wizState, v, e map[string]string, secret string) (wizView, error) {
	ctx := r.Context()
	_, idx, _ := wizFind(st.Slug)
	view := wizView{Step: st, Num: idx + 1, Total: len(wizSteps), Done: ws.done(st.Slug), Import: ws.mode == "import",
		Prev: wizHref(idx - 1), Next: wizHref(idx + 1), Pct: idx * 100 / (len(wizSteps) - 1)}
	view.Verify = view.Import && view.Done
	if v == nil {
		var err error
		if v, err = s.wizDefaults(r, st.Slug, ws); err != nil {
			return view, err
		}
	}
	if e == nil {
		e = map[string]string{}
	}
	view.Form = formPage{Action: "/admin/setup/" + st.Slug}
	switch st.Slug {
	case "company":
		view.Form.Fields = wizCompanyFields(v, e)
	case "router":
		view.Form.Fields = wizRouterFields(v, e)
		if mode := v["router_mode"]; mode != "api" && v["nas_ip"] != "" {
			view.Commands = radiusCommands(s.wizHost(), v["nas_ip"], secret)
			view.LiveURL = "/admin/setup/live.json?nas=" + url.QueryEscape(v["nas_ip"])
		}
	case "bandwidth":
		view.Form.Fields = wizSpeedFields(v, e)
	case "plan":
		fields, err := s.wizPlanFields(ctx, ws, v, e)
		if err != nil {
			return view, err
		}
		view.Form.Fields = fields
	case "whatsapp":
		view.Form.Fields = wizWhatsAppFields(v, e, ws.mode == "import")
	case "qris":
		view.Form.Fields = wizQRISFields(v, e)
	case "tripay":
		view.Form.Fields = wizTripayFields(v, e, ws.st)
	case "isolir":
		view.Form.Fields = append([]field{{Name: "isolir_on", Label: "Turn on Isolir", Type: "checkbox", Checked: v["isolir_on"] == "1",
			Section: "Isolir", Hint: "Customers whose plan has expired are sent to the isolir page."}}, s.isolirSetupFields(ws.st)...)
	case "customer":
		plans, err := s.queries.ListPlans(ctx, db.ListPlansParams{Limit: 1000})
		if err != nil {
			return view, err
		}
		f := text("voucher_plan", "Plan for the test voucher", v, e).as("select").req()
		f.Hint = "Creates one test voucher code. Real customers can be added later on the Customers page."
		for _, p := range plans {
			f.Options = append(f.Options, option{strconv.FormatInt(p.ID, 10), p.Name})
		}
		view.Form.Fields = section([]field{f}, "Test voucher", "")
	case "finish":
		for _, x := range wizSteps {
			if x.Optional || x.Slug == "finish" {
				continue
			}
			view.Summary = append(view.Summary, wizLine{Label: x.Title, OK: ws.done(x.Slug)})
		}
		view.Voucher = ws.raw[setupVoucher]
		if view.Voucher != "" {
			view.LiveURL = "/admin/setup/live.json?voucher=" + url.QueryEscape(view.Voucher)
		}
	}
	view.Secret = secret != ""
	return view, nil
}

// wizHost is the LAN address the routers reach gobill on (see gobillAddr).
func (s *Server) wizHost() string {
	host, _ := s.gobillAddr()
	return host
}

type wizLine struct {
	Label string
	OK    bool
}

// wizView is the data of the setup template.
type wizView struct {
	Step       wizStep
	Num, Total int
	Pct        int
	Done       bool
	Import     bool // came from a PHPNuxBill import
	Verify     bool // imported and done: the step shows the data to check
	Prev, Next string
	Form       formPage
	Secret     bool     // the NAS secret was saved just now and is in the commands
	Commands   []string // RouterOS commands (router step)
	LiveURL    string   // JSON the live indicator polls
	Summary    []wizLine
	Voucher    string
}

func wizCompanyFields(v, e map[string]string) []field {
	return section([]field{
		text("company_name", "Company Name", v, e).req().hint("Name of your business. Shown on invoices, messages and the login page."),
		text("app_url", "App URL", v, e).hint("Public address of this app, e.g. https://billing.example.com. Used for links in WhatsApp messages (QRIS, invoice)."),
		text("phone", "Phone Number", v, e).hint("Operator phone or WhatsApp. Shown to customers on the portal."),
	}, "Business", "")
}

func wizRouterFields(v, e map[string]string) []field {
	mode := text("router_mode", "How does gobill connect to MikroTik?", v, e).as("select").bind().req()
	mode.Options = []option{{"radius", "RADIUS (recommended)"}, {"api", "API"}, {"both", "Both"}}
	out := section([]field{mode}, "Connection", "")
	nasSecret := text("nas_secret", "Shared Secret", v, e).as("password").hint("At least 16 characters. Use Generate, then copy the commands before you leave this page.")
	nasSecret.Value, nasSecret.Gen = "", true
	out = append(out, section([]field{
		text("nas_name", "NAS Name", v, e).req(),
		text("nas_ip", "IP / CIDR", v, e).req().hint("The MikroTik address that talks to gobill, e.g. 192.168.88.1."),
		nasSecret,
	}, "NAS (RADIUS)", "router_mode !== 'api'")...)
	pw := text("router_password", "Password", v, e).as("password").hint("Leave empty to keep the current password")
	pw.Value = ""
	out = append(out, section([]field{
		text("router_name", "Router Name", v, e).req(),
		text("router_host", "Host", v, e).req().hint("IP address or hostname of the MikroTik."),
		text("router_port", "Port", v, e).as("number").req(),
		text("router_username", "Username", v, e).req(),
		pw,
		{Name: "router_test", Type: "test", Value: "/admin/setup/router/test", Btn: "Test connection", Hint: "Saves the router first, then checks the connection."},
	}, "Router (API)", "router_mode !== 'radius'")...)
	return out
}

func wizSpeedFields(v, e map[string]string) []field {
	var fs []field
	for i := range wizSpeeds {
		f := text(speedKey(i), "Speed (Mbps)", v, e).as("number")
		f.Hint = "Empty = skip this speed."
		fs = append(fs, f)
	}
	return section(fs, "Speeds", "")
}

func (s *Server) wizPlanFields(ctx context.Context, ws wizState, v, e map[string]string) ([]field, error) {
	bws, err := s.queries.ListBandwidths(ctx, db.ListBandwidthsParams{Limit: 1000})
	if err != nil {
		return nil, err
	}
	bw := text("plan_bandwidth", "Speed for these plans", v, e).as("select").req()
	for _, b := range bws {
		bw.Options = append(bw.Options, option{strconv.FormatInt(b.ID, 10), b.Name})
	}
	out := []field{bw}
	if ws.effectiveRouter() == "api" {
		routers, err := s.queries.ListRouters(ctx, db.ListRoutersParams{Limit: 1000})
		if err != nil {
			return nil, err
		}
		rt := text("plan_router", "Router", v, e).as("select").req()
		for _, x := range routers {
			rt.Options = append(rt.Options, option{strconv.FormatInt(x.ID, 10), x.Name})
		}
		out = append(out, rt)
	}
	for i, p := range wizPlans {
		f := text(priceKey(i), p.Label, v, e).as("number")
		f.Hint = "Empty = skip this plan."
		out = append(out, f)
	}
	return section(out, "Plan templates", ""), nil
}

func wizWhatsAppFields(v, e map[string]string, importing bool) []field {
	pw := text("alt_wga_password", "Password", v, e).as("password").hint("Leave empty to keep the saved password")
	pw.Value = ""
	out := []field{
		text("alt_wga_server_url", "WA server URL", v, e).hint("Address of your GOWA gateway, e.g. http://127.0.0.1:3000"),
		text("alt_wga_device_id", "Device ID", v, e),
		text("alt_wga_username", "Username", v, e),
		pw,
	}
	if importing {
		n := text("notify_customers", "Send notifications to customers", v, e).as("select")
		n.Options = []option{{"yes", "Yes"}, {"no", "No"}}
		out = append(out, n.hint("No stops every message to customers. Operator alerts are not affected."))
	}
	out = append(out, field{Name: "wa_test_phone", Label: "Test number", Type: "test", Inp: "tel", Value: "/admin/setup/whatsapp/test",
		Btn: "Send test message", Error: e["wa_test_phone"], Hint: "Enter a number to receive a test message."})
	return section(out, "WhatsApp (GOWA)", "")
}

func wizQRISFields(v, e map[string]string) []field {
	qris := field{Name: "qris_payload", Label: "Static QRIS", Type: "qris", Value: v["qris_payload"], Error: e["qris_payload"],
		Hint: "Upload the merchant's static QRIS photo (PNG/JPG). The system only stores its text"}
	if m, n := payment.QRISInfo(v["qris_payload"]); m != "" {
		qris.Hint = "Active: " + m
		if n != "" {
			qris.Hint += " (NMID " + n + ")"
		}
		if png, err := qrcode.Encode(v["qris_payload"], qrcode.Medium, 256); err == nil {
			qris.Img = wizDataURI(png)
		}
	}
	return section([]field{qris}, "QRIS", "")
}

func wizTripayFields(v, e map[string]string, st map[string]string) []field {
	on := field{Name: "tripay_on", Label: "Use Tripay for payments", Type: "checkbox", Checked: v["tripay_on"] == "1"}
	sec := func(name, label string) field {
		f := text(name, label, nil, e).as("password")
		f.Hint = "Leave empty to keep the current secret"
		if st[name] != "" {
			f.Hint = "Saved. Leave empty to keep the current secret"
		}
		return f
	}
	mode := text("tripay_mode", "Tripay Mode", v, e).as("select")
	mode.Options = []option{{"sandbox", "Sandbox"}, {"production", "Production"}}
	return section([]field{on, sec("tripay_api_key", "Tripay API Key"), sec("tripay_private_key", "Tripay Private Key"),
		text("tripay_merchant_code", "Tripay Merchant Code", v, e), mode,
		text("tripay_channel", "Default Channel", v, e).hint("Optional channel code, e.g. QRIS")}, "Tripay", "")
}
