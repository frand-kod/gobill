package web

// ISOLIR: the public page an expired customer sees (identified by the client IP of the RADIUS
// session), its voucher form, the custom HTML mode, the settings preview and reset, and the
// MikroTik setup helper. Settings are SuperAdmin only (see settingsTabs and routes).

import (
	"context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
	"github.com/frand-kod/gobill/internal/notify"
)

// isolirCSP sandboxes the custom HTML: no allow-same-origin, so admin scripts cannot use gobill cookies.
const isolirCSP = "sandbox allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox"

// isolirVoucherForm is the voucher box; it posts without a session (see routes: CSRF bypass).
func (s *Server) isolirVoucherForm() template.HTML {
	t := func(text string) string { return s.catalog.T(s.language(), text) }
	return template.HTML(`<form method="post" action="/isolir/voucher" autocomplete="off"><input name="code" placeholder="` + t("Voucher code") + `" autocapitalize="characters" required><p><button class="btn" type="submit">` + t("Activate voucher") + `</button></p></form>`)
}

// isolirView is what the page shows. Known is false for a client IP with no open session.
type isolirView struct {
	Known       bool
	CustomerID  int64
	Name        string
	Username    string
	PlanName    string
	ExpiredAt   string
	Amount      string
	WAAdmin     string
	LogoutURL   string // hotspot logout link, "" when isolir_hotspot_url is not set
	Title       string
	Message     string
	Logo        string
	Company     string
	ShowVoucher bool
	VoucherForm template.HTML
	Promos      template.HTML
	Notice      string
}

// isolirPage is GET /isolir. Unknown clients get the generic page.
func (s *Server) isolirPage(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "isolir settings", err)
		return
	}
	s.writeIsolir(w, r, st, s.isolirFor(r.Context(), clientIP(r), st, ""), http.StatusOK)
}

// isolirVoucher is POST /isolir/voucher. It redeems for the customer on this client IP, rate limited
// per IP like the portal voucher form.
func (s *Server) isolirVoucher(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "isolir settings", err)
		return
	}
	ip := clientIP(r)
	key := "iso:" + ip
	if s.tooManyFailures(key) {
		s.writeIsolir(w, r, st, s.isolirFor(r.Context(), ip, st, "Too many attempts. Try again in 15 minutes."), http.StatusTooManyRequests)
		return
	}
	v := s.isolirFor(r.Context(), ip, st, "")
	if !v.Known {
		s.writeIsolir(w, r, st, v, http.StatusOK, "Enter the voucher on the hotspot login page.")
		return
	}
	if s.Billing == nil {
		s.fail(w, "isolir voucher", errors.New("billing not configured"))
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	notice := "Voucher activated. Try opening the internet again."
	switch err := s.Billing.RedeemVoucher(r.Context(), code, v.CustomerID); {
	case code == "" || errors.Is(err, billing.ErrVoucherInvalid):
		s.recordFailure(key)
		notice = "Voucher is not valid."
	case errors.Is(err, billing.ErrInactive):
		notice = "Account is not active."
	case err != nil:
		s.fail(w, "isolir voucher", err)
		return
	}
	// the page is rebuilt so the plan reflects the redeem
	s.writeIsolir(w, r, st, s.isolirFor(r.Context(), ip, st, notice), http.StatusOK)
}

// isolirFor identifies the customer of a client IP: the most recent open RADIUS session on that
// framed IP, its customer, and the last plan that is not the isolir plan.
func (s *Server) isolirFor(ctx context.Context, ip string, st map[string]string, notice string) isolirView {
	v := isolirView{Notice: notice}
	c, ok := s.isolirCustomer(ctx, ip)
	if !ok {
		return v
	}
	v.Known, v.CustomerID, v.Username = true, c.ID, c.Username
	v.Name = c.Fullname
	if v.Name == "" {
		v.Name = c.Username
	}
	subs, err := s.queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 1000})
	if err != nil {
		return v
	}
	isoID := isolir.PlanID(st["isolir_plan_id"])
	for _, sub := range subs {
		if sub.PlanID == isoID {
			continue
		}
		v.ExpiredAt = time.Unix(sub.ExpiresAt, 0).In(s.location()).Format("2006-01-02 15:04")
		if p, err := s.queries.GetPlan(ctx, sub.PlanID); err == nil {
			v.PlanName, v.Amount = p.Name, money(p.Price)
		}
		break
	}
	return v
}

// isolirCustomer finds the customer of a client IP: the owner of the most recent open RADIUS session
// on that framed IP. ok is false when there is none.
func (s *Server) isolirCustomer(ctx context.Context, ip string) (c db.Customer, ok bool) {
	sess, err := s.queries.GetOpenRadiusSessionByFramedIP(ctx, ip)
	if err != nil {
		return c, false
	}
	c, err = s.queries.GetCustomerByUsername(ctx, sess.Username)
	return c, err == nil
}

// isolirIsolated reports whether the customer has an isolation that is running now.
func (s *Server) isolirIsolated(ctx context.Context, customerID int64) bool {
	_, err := s.queries.GetRunningIsolation(ctx, db.GetRunningIsolationParams{CustomerID: customerID, Now: time.Now().Unix()})
	return err == nil
}

// isolirGate serves the isolir page to an isolated client for GET and HEAD requests, whatever Host
// and path it asked for. It wraps the whole router, so it runs before the root redirect. Paths the
// client needs to pay or log in stay reachable (isolirExempt). With isolir off it does no query.
func (s *Server) isolirGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isolir.Load() || (r.Method != http.MethodGet && r.Method != http.MethodHead) || isolirExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		c, ok := s.isolirCustomer(r.Context(), clientIP(r))
		if !ok || !s.isolirIsolated(r.Context(), c.ID) {
			next.ServeHTTP(w, r)
			return
		}
		s.isolirPage(w, r)
	})
}

// isolirExempt is true for the paths an isolated client still needs: the isolir page, static and
// uploaded files, QRIS images, the customer login and portal (payment) and the health check.
func isolirExempt(p string) bool {
	if p == "/isolir" || p == "/login" || p == "/portal" || p == "/health" {
		return true
	}
	for _, dir := range []string{"/isolir/", "/static/", "/uploads/", "/qris/", "/portal/"} {
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	return false
}

// writeIsolir fills the parts every render shares and writes the page: the custom HTML when the
// mode is custom and it is not blank, else the default layout. Custom HTML is sandboxed.
func (s *Server) writeIsolir(w http.ResponseWriter, r *http.Request, st map[string]string, v isolirView, status int, notice ...string) {
	if len(notice) > 0 {
		v.Notice = notice[0]
	}
	v.Title = isolir.Value(st, "isolir_title")
	v.Message = isolir.Value(st, "isolir_message")
	v.Company = st["company_name"]
	if st["logo"] != "" {
		v.Logo = "/uploads/" + st["logo"]
	}
	v.ShowVoucher = isolir.Value(st, "isolir_show_voucher") != "no"
	if v.ShowVoucher {
		v.VoucherForm = s.isolirVoucherForm()
	}
	v.Promos = s.promoBlocks(r.Context(), "isolir")
	v.WAAdmin = isolirWALink(v, isolir.Value(st, "isolir_wa"))
	v.LogoutURL = isolirLogoutURL(st)

	custom := isolir.Value(st, "isolir_mode") == "custom" && strings.TrimSpace(st["isolir_html"]) != ""
	s.emitIsolir(w, st, v, status, custom, st["isolir_html"])
}

func (s *Server) emitIsolir(w http.ResponseWriter, st map[string]string, v isolirView, status int, custom bool, src string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if custom {
		w.Header().Set("Content-Security-Policy", isolirCSP)
		w.WriteHeader(status)
		w.Write([]byte(renderIsolirCustom(src, v)))
		return
	}
	w.WriteHeader(status)
	if err := s.isolirDefault().Execute(w, v); err != nil {
		s.fail(w, "isolir page", err)
	}
}

// isolirLogoutURL is the hotspot logout address: isolir_hotspot_url without a trailing slash, plus
// /logout. "" when the setting is empty, which hides the button.
func isolirLogoutURL(st map[string]string) string {
	base := strings.TrimRight(strings.TrimSpace(st["isolir_hotspot_url"]), "/")
	if base == "" {
		return ""
	}
	return base + "/logout"
}

// isolirWALink is the wa.me link with the customer, plan and amount filled in; "" without a number.
func isolirWALink(v isolirView, phone string) string {
	link := waLink(phone, "62")
	if link == "" {
		return ""
	}
	text := "Halo admin, paket internet saya sudah habis"
	if v.Known {
		text += " (" + v.Name + ", " + v.Username + ", paket " + v.PlanName + ")"
	}
	return link + "?text=" + url.QueryEscape(text+". Mohon bantuannya.")
}

// isolirTokenRe matches the [[name]] variables of the custom HTML.
var isolirTokenRe = regexp.MustCompile(`\[\[([a-z_]+)\]\]`)

// renderIsolirCustom replaces the variables. Values are HTML-escaped except voucher_form and promos,
// which the system renders. Unknown or empty variables become "".
func renderIsolirCustom(src string, v isolirView) string {
	known := "no"
	if v.Known {
		known = "yes"
	}
	vals := map[string]string{
		"company_name": v.Company, "logo_url": v.Logo, "username": v.Username, "name": v.Name,
		"plan_name": v.PlanName, "expired_at": v.ExpiredAt, "amount": v.Amount,
		"wa_admin_link": v.WAAdmin, "voucher_form": string(v.VoucherForm), "promos": string(v.Promos),
		"known": known, "title": v.Title, "message": v.Message, "notice": v.Notice, "logout_url": v.LogoutURL,
	}
	return isolirTokenRe.ReplaceAllStringFunc(src, func(m string) string {
		name := m[2 : len(m)-2]
		x := vals[name]
		if name == "voucher_form" || name == "promos" {
			return x
		}
		return html.EscapeString(x)
	})
}

// isolirDefault is the default layout: phone first, no scripts, no external assets.
const isolirDefaultSrc = `<!doctype html>
<html lang="id"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
body{margin:0;font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;background:#f4f4f5;color:#18181b}
main{max-width:440px;margin:0 auto;padding:16px}
.card{background:#fff;border:1px solid #e4e4e7;border-radius:12px;padding:16px;margin:0 0 12px}
h1{font-size:1.3rem;margin:.2rem 0 .5rem}h2{font-size:1.05rem;margin:0 0 .5rem}
p{margin:.4rem 0}.muted{color:#52525b;font-size:.9rem}
.btn{display:block;box-sizing:border-box;width:100%;text-align:center;background:#16a34a;color:#fff;text-decoration:none;border:0;border-radius:8px;padding:12px;margin:8px 0;font:600 1rem system-ui,sans-serif;cursor:pointer}
.btn-ghost{background:#fff;color:#18181b;border:1px solid #d4d4d8}
input{display:block;box-sizing:border-box;width:100%;padding:10px;border:1px solid #d4d4d8;border-radius:8px;font:1rem system-ui,sans-serif}
img.logo{display:block;max-height:48px;max-width:180px;margin-bottom:8px}
</style></head><body><main>
{{if .Logo}}<img class="logo" src="{{.Logo}}" alt="{{.Company}}">{{end}}
{{if .Notice}}<div class="card"><p>{{T .Notice}}</p></div>{{end}}
<div class="card"><h1>{{.Title}}</h1>
{{if .Known}}<p>{{T "Hello"}} <b>{{.Name}}</b></p><p class="muted">{{T "Last plan"}}: {{.PlanName}}<br>{{T "Ends"}}: {{.ExpiredAt}}</p>{{end}}
<p>{{.Message}}</p></div>
{{if .WAAdmin}}<a class="btn" href="{{.WAAdmin}}" rel="noopener">{{T "Contact admin"}}</a>{{end}}
{{if .LogoutURL}}<a class="btn btn-ghost" href="{{.LogoutURL}}" rel="noopener">{{T "Log out / use another account"}}</a>{{end}}
{{if .ShowVoucher}}<div class="card"><h2>{{T "Have a voucher?"}}</h2>{{.VoucherForm}}</div>{{end}}
{{.Promos}}
</main></body></html>`

// isolirDefault parses the default layout with T bound to the request language.
func (s *Server) isolirDefault() *template.Template {
	return template.Must(template.New("isolir").Funcs(template.FuncMap{"T": func(text string) string { return s.catalog.T(s.language(), text) }}).Parse(isolirDefaultSrc))
}

// isolirPreview is GET /admin/settings/isolir/preview: the page with sample data, in a new tab. The
// custom HTML is used when it is not blank, so a draft can be checked after saving.
func (s *Server) isolirPreview(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadSettings(r.Context())
	if err != nil {
		s.fail(w, "isolir settings", err)
		return
	}
	v := isolirView{Known: true, CustomerID: 0, Name: "Budi", Username: "budi", PlanName: "Plan 10 Mbps",
		ExpiredAt: "2026-01-31 23:59", Amount: money(150000), ShowVoucher: true}
	v.Title = isolir.Value(st, "isolir_title")
	v.Message = isolir.Value(st, "isolir_message")
	v.Company = st["company_name"]
	if st["logo"] != "" {
		v.Logo = "/uploads/" + st["logo"]
	}
	v.VoucherForm = s.isolirVoucherForm()
	v.Promos = s.promoBlocks(r.Context(), "isolir")
	v.WAAdmin = isolirWALink(v, isolir.Value(st, "isolir_wa"))
	v.LogoutURL = isolirLogoutURL(st)
	v.Notice = "Sample view. Customer data is not real."
	s.emitIsolir(w, st, v, http.StatusOK, strings.TrimSpace(st["isolir_html"]) != "", st["isolir_html"])
}

// isolirSections are the defaults each "Kembalikan ke default" action writes.
var isolirSections = map[string]map[string]string{
	"paket":   {"isolir_enabled": "no", "isolir_plan_id": "", "isolir_days": "30"},
	"halaman": {"isolir_title": isolir.Defaults["isolir_title"], "isolir_message": isolir.Defaults["isolir_message"], "isolir_wa": "", "isolir_show_voucher": "yes", "isolir_address_list": isolir.DefaultAddrList},
	"mode":    {"isolir_mode": "default"},
	"html":    {"isolir_html": ""},
}

// isolirReset is POST /admin/settings/isolir/reset/{section}: writes the section's defaults.
func (s *Server) isolirReset(w http.ResponseWriter, r *http.Request) {
	sec := r.PathValue("section")
	set, ok := isolirSections[sec]
	if !ok {
		http.NotFound(w, r)
		return
	}
	for k, v := range set {
		if err := s.queries.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			s.fail(w, "isolir reset "+k, err)
			return
		}
	}
	if s.SettingsChanged != nil {
		s.SettingsChanged(r.Context())
	}
	s.done(w, r, "/admin/settings/isolir", "Settings saved", "settings.reset", "isolir "+sec)
}

// isolirVarList are the variables of the custom HTML, the ones renderIsolirCustom fills.
var isolirVarList = []notify.Var{
	{Name: "company_name", Desc: "Company name"},
	{Name: "logo_url", Desc: "Logo address"},
	{Name: "title", Desc: "Title of the page (Title setting)"},
	{Name: "message", Desc: "Message of the page (Message setting)"},
	{Name: "username", Desc: "Customer's username"},
	{Name: "name", Desc: "Customer's full name"},
	{Name: "plan_name", Desc: "Package name"},
	{Name: "expired_at", Desc: "Expiry date and time"},
	{Name: "amount", Desc: "Price of the package"},
	{Name: "logout_url", Desc: "Hotspot logout address, empty when the Hotspot address is not set"},
	{Name: "wa_admin_link", Desc: "WhatsApp link to the admin"},
	{Name: "voucher_form", Desc: "Voucher form, rendered by gobill"},
	{Name: "promos", Desc: "Promotions, rendered by gobill"},
	{Name: "known", Desc: "yes or no: the client is a known customer"},
	{Name: "notice", Desc: "Result of the last action, such as a voucher"},
}

// isolirRouterRules is the one block of RouterOS commands that isolates a client except the
// allow-list. The rules are appended to the forward chain, so they must sit above any general
// accept rule. Every allow-list entry gets a tls-host rule that learns its IPs from the TLS SNI;
// exact hosts are also added to the allow address-list up front.
func isolirRouterRules(list, host, port string, hosts []string) string {
	allow := list + "-allow"
	var b strings.Builder
	fmt.Fprintf(&b, "/ip firewall nat add chain=dstnat src-address-list=%s dst-address-type=!local protocol=tcp dst-port=80 action=dst-nat to-addresses=%s to-ports=%s\n", list, host, port)
	fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s dst-address=%s action=accept\n", list, host)
	for _, h := range hosts {
		if !strings.HasPrefix(h, "*.") {
			fmt.Fprintf(&b, "/ip firewall address-list add list=%s address=%s comment=\"gobill isolir allow\"\n", allow, h)
		}
	}
	for _, h := range hosts {
		fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s protocol=tcp dst-port=443 tls-host=%s action=add-dst-to-address-list address-list=%s address-list-timeout=1d comment=\"isolir: learn %s\"\n", list, h, allow, h)
	}
	fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s protocol=tcp dst-port=443 tcp-flags=syn action=accept comment=\"isolir: TLS handshake\"\n", list)
	fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s dst-address-list=%s action=accept comment=\"isolir: allowed apps\"\n", list, allow)
	fmt.Fprintf(&b, "/ip firewall filter add chain=forward src-address-list=%s action=drop comment=\"isolir: block the rest\"", list)
	return b.String()
}

// isolirSetupFields is the MikroTik setup helper: copyable RouterOS commands for the address gobill
// listens on (see gobillAddr).
func (s *Server) isolirSetupFields(st map[string]string) []field {
	t := func(text string) string { return s.catalog.T(s.language(), text) }
	host, port := s.gobillAddr()
	list := isolir.Value(st, "isolir_address_list")
	hosts := isolir.HostLines(isolir.Value(st, "isolir_allow_hosts"))
	sec := t("Setup router (MikroTik, RouterOS 6 and 7)")
	return []field{
		{Name: "isolir_cmd_rules", Label: t("1. Isolation rules (paste the whole block, in this order)"), Type: "pre", Section: sec,
			Value: isolirRouterRules(list, host, port, hosts),
			Hint:  t("The rules must sit above any general accept rule in the forward chain, or isolated clients are not cut off. Only HTTP on port 80 is redirected to the isolir page. Hosts in Allowed while isolated are reachable; wildcard hosts are learned when the app opens a TLS connection. Changed the allowed hosts? Paste the block again.")},
		{Name: "isolir_cmd_walled", Label: t("2. Walled garden hotspot for gobill"), Type: "pre", Section: sec,
			Value: fmt.Sprintf("/ip hotspot walled-garden ip add dst-address=%s dst-port=%s action=accept", host, port),
			Hint:  t("So the isolir page opens before hotspot login.")},
		{Name: "isolir_cmd_bypass", Label: t("3. Bypass the hotspot for the gobill host"), Type: "pre", Section: sec,
			Value: "/ip hotspot ip-binding add mac-address=<MAC-HOST-GOBILL> type=bypassed",
			Hint:  t("Replace <MAC-HOST-GOBILL> with the MAC address of gobill's LAN card. Without this rule, the hotspot can capture gobill's own traffic.")},
		{Name: "isolir_cmd_test", Label: t("Test from the LAN with curl"), Type: "pre", Section: sec,
			Value: fmt.Sprintf(`curl -i -H "Host: example.com" http://%s:%s/`, host, port),
			Hint:  t("gobill serves the isolir page to isolated clients at the root and at every other path. Do not test with /tool fetch: RouterOS sends a duplicate Host header and gets 400.")},
		{Name: "isolir_cmd_note", Label: t("Note"), Type: "note", Section: sec,
			Hint: t("The address-list is sent by RADIUS as Mikrotik-Address-List. RouterOS 6 resolves DNS allow-list entries to exact names only, so a host whose CDN address changes may sometimes fail, such as WhatsApp media. Add a bank app by putting its host in Allowed while isolated, then paste the block again.")},
	}
}

// gobillAddr is the LAN address and port gobill listens on (GOBILL_HTTP). A host that is not an IP,
// such as empty or 0.0.0.0, gives the first LAN IPv4 address of this host.
func (s *Server) gobillAddr() (host, port string) {
	addr := s.ListenAddr
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", "8080"
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = lanIPv4()
	}
	return host, port
}

// lanIPv4 is the first non-loopback IPv4 address of this host, or a placeholder when there is none.
func lanIPv4() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			return ipn.IP.String()
		}
	}
	return "<IP-LAN-GOBILL>"
}
