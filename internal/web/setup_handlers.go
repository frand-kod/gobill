package web

// Quick Setup: routes, handlers and the save of each step.
// The wizard owns no data. Each save replays the existing handler of that feature (NAS, router,
// bandwidth) or goes through the same services the admin pages use (service/setup, service/plan), so
// validation and side effects stay in one place. Progress is read back from the rows.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"github.com/frand-kod/gobill/internal/settings"
	"github.com/frand-kod/gobill/internal/web/httpx"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
	"github.com/frand-kod/gobill/internal/service/setup"
)

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
	warn := s.proxyWarning(r)
	if st.Slug == "router" { // a tunnel the routers cannot reach is worth a word before they are set up
		if wgw := s.wgWarning(r.Context()); wgw != "" {
			warn = strings.TrimSpace(warn + " " + wgw)
		}
	}
	s.render(w, r, http.StatusOK, "setup", Page{Title: "Quick setup", Data: view, Warn: warn})
}

// setupStart answers the first-run dialog. to is "new" (Mulai baru), "import" or "" (Mulai setup cepat).
func (s *Server) setupStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur := settings.Get(ctx, s.queries, setupKey)
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
		open, err := s.queries.ListOpenRadiusSessionsByUser(r.Context(), code)
		if err != nil {
			s.fail(w, "setup live", err)
			return
		}
		out["voucher_online"] = len(open) > 0
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
			s.sessions.Put(ctx, "flash", s.t("Saved"))
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
			page.Error = s.t("Please check the highlighted fields")
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

// wizPut writes settings the way the Settings pages do, see setup.Service.Put.
func (s *Server) wizPut(ctx context.Context, kv map[string]string) error {
	return s.setup().Put(ctx, kv)
}

func (s *Server) setup() *setup.Service {
	return &setup.Service{Q: s.queries, PlanSvc: s.Plans, Langs: s.catalog.Languages(), Saved: s.settingsSaved}
}

func (s *Server) wizSaveCompany(r *http.Request) (wizResult, error) {
	v := httpx.FormVals(r, "company_name", "app_url", "phone")
	e, err := s.setup().Company(r.Context(), v)
	if err != nil {
		return wizResult{}, err
	}
	if len(e) > 0 {
		return wizResult{v: v, e: e}, nil
	}
	return wizResult{next: true}, nil
}

// wizSaveRouter saves the NAS (RADIUS) and/or the router (API). An existing row with the same IP or
// host is edited, never duplicated. The step stays open so the commands and the live indicator show.
func (s *Server) wizSaveRouter(r *http.Request, kind int) (wizResult, error) {
	ctx := r.Context()
	v := httpx.FormVals(r, "router_mode", "nas_name", "nas_ip", "nas_secret", "router_name", "router_host", "router_port", "router_username", "router_password")
	e := map[string]string{}
	res := wizResult{v: v, e: e}
	mode := v["router_mode"]
	if !httpx.OneOf(mode, "radius", "api", "both") {
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
	s.sessions.Put(ctx, "flash", s.t("Saved"))
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
		if _, ok := httpx.PosInt(rate); !ok {
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
	v := httpx.FormVals(r, "plan_bandwidth", "plan_router")
	in := setup.PlansInput{Bandwidth: v["plan_bandwidth"], Router: v["plan_router"], RouterAPI: ws.effectiveRouter() == "api"}
	for i, p := range wizPlans {
		v[priceKey(i)] = strings.TrimSpace(r.PostFormValue(priceKey(i)))
		in.Templates = append(in.Templates, setup.PlanTemplate{Key: priceKey(i), Name: p.Name, Price: v[priceKey(i)], Validity: p.Validity, Unit: p.Unit})
	}
	created, e, err := s.setup().Plans(ctx, in)
	if err != nil {
		return wizResult{}, err
	}
	for _, p := range created { // what the Plans page does after a save
		s.syncPlan(r, "add", p, "")
		s.logActivity(r, "plan.create", p.Name)
	}
	for k, msg := range e {
		e[k] = s.t(msg)
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
	err := s.setup().WhatsApp(r.Context(), setup.WhatsApp{URL: r.PostFormValue("alt_wga_server_url"), Device: r.PostFormValue("alt_wga_device_id"),
		User: r.PostFormValue("alt_wga_username"), Password: r.PostFormValue("alt_wga_password"), Notify: r.PostFormValue("notify_customers")})
	if err != nil {
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
		return wizResult{v: v, e: map[string]string{"qris_payload": qerr}}, nil
	}
	e, err := s.setup().QRIS(r.Context(), current, v["qris_payload"])
	if err != nil {
		return wizResult{}, err
	}
	if len(e) > 0 {
		return wizResult{v: v, e: e}, nil
	}
	return wizResult{next: true}, nil
}

func (s *Server) wizSaveTripay(r *http.Request) (wizResult, error) {
	pf := r.PostFormValue
	kv, e, err := s.setup().Tripay(r.Context(), setup.Tripay{On: pf("tripay_on") == "1", Merchant: pf("tripay_merchant_code"), Mode: pf("tripay_mode"),
		Channel: pf("tripay_channel"), APIKey: pf("tripay_api_key"), PrivateKey: pf("tripay_private_key")})
	if err != nil {
		return wizResult{}, err
	}
	if len(e) > 0 {
		return wizResult{v: kv, e: e}, nil
	}
	return wizResult{next: true}, nil
}

// wizSaveIsolir turns Isolir on or off. The isolir plan is created the first time, as the Settings page does.
func (s *Server) wizSaveIsolir(r *http.Request) (wizResult, error) {
	if err := s.setup().Isolir(r.Context(), r.PostFormValue("isolir_on") == "1"); err != nil {
		return wizResult{}, err
	}
	return wizResult{next: true}, nil
}

// wizSaveVoucher makes the one test voucher, unless the saved one still exists.
func (s *Server) wizSaveVoucher(r *http.Request) (wizResult, error) {
	ctx := r.Context()
	v := httpx.FormVals(r, "voucher_plan")
	e := map[string]string{}
	planID, ok := httpx.PosInt(v["voucher_plan"])
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
	if cur := settings.Get(ctx, s.queries, setupVoucher); cur != "" {
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
			Section: "Isolir", Hint: "RADIUS customers whose plan has expired are isolated and see the isolir page."}}, s.isolirSetupFields(ws.st)...)
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
