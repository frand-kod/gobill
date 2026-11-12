package public

// ISOLIR: the public page an expired customer sees (identified by the client IP of the RADIUS
// session), its voucher form, the custom HTML mode, the settings preview and reset, and the
// MikroTik setup helper. Settings are SuperAdmin only (see settingsTabs and routes).

import (
	"context"
	"errors"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/view"

	"github.com/frand-kod/gobill/internal/billing"
	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
)

// isolirCSP sandboxes the custom HTML: no allow-same-origin, so admin scripts cannot use gobill cookies.
const isolirCSP = "sandbox allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox"

// isolirVoucherForm is the voucher box; it posts without a session (see routes: CSRF bypass).
func (s *Handlers) isolirVoucherForm() template.HTML {
	return template.HTML(`<form method="post" action="/isolir/voucher" autocomplete="off"><label for="isolir-code" class="muted">` + s.T("Voucher code") + `</label><input id="isolir-code" name="code" placeholder="` + s.T("Voucher code") + `" autocapitalize="characters" required><p><button class="btn" type="submit">` + s.T("Activate voucher") + `</button></p></form>`)
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
	PortalURL   string // customer login, where the package is paid or extended
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
func (s *Handlers) isolirPage(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "isolir settings", err)
		return
	}
	s.writeIsolir(w, r, st, s.isolirFor(r.Context(), core.ClientIP(r), st, ""), http.StatusOK)
}

// isolirVoucher is POST /isolir/voucher. It redeems for the customer on this client IP, rate limited
// per IP like the portal voucher form.
func (s *Handlers) isolirVoucher(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "isolir settings", err)
		return
	}
	ip := core.ClientIP(r)
	key := "iso:" + ip
	if s.Reserve(key, core.MaxFailedLogins) < 0 {
		s.writeIsolir(w, r, st, s.isolirFor(r.Context(), ip, st, "Too many attempts. Try again in 15 minutes."), http.StatusTooManyRequests)
		return
	}
	v := s.isolirFor(r.Context(), ip, st, "")
	if !v.Known {
		s.Unreserve(key)
		s.writeIsolir(w, r, st, v, http.StatusOK, "Enter the voucher on the hotspot login page.")
		return
	}
	if s.Billing == nil {
		s.Unreserve(key)
		s.Fail(w, "isolir voucher", errors.New("billing not configured"))
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	notice := "Voucher activated. Try opening the internet again."
	switch err := s.Billing.RedeemVoucher(r.Context(), code, v.CustomerID); {
	case code == "" || errors.Is(err, billing.ErrVoucherInvalid):
		notice = "Voucher is not valid."
	case errors.Is(err, billing.ErrInactive):
		s.Unreserve(key)
		notice = "Account is not active."
	case err != nil:
		s.Unreserve(key)
		s.Fail(w, "isolir voucher", err)
		return
	default:
		s.Unreserve(key)
	}
	// the page is rebuilt so the plan reflects the redeem
	s.writeIsolir(w, r, st, s.isolirFor(r.Context(), ip, st, notice), http.StatusOK)
}

// isolirFor identifies the customer of a client IP: the most recent open RADIUS session on that
// framed IP, its customer, and the last plan that is not the isolir plan.
func (s *Handlers) isolirFor(ctx context.Context, ip string, st map[string]string, notice string) isolirView {
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
	subs, err := s.Queries.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: c.ID, Limit: 1000})
	if err != nil {
		return v
	}
	isoID := isolir.PlanIDIn(st)
	for _, sub := range subs {
		if sub.PlanID == isoID {
			continue
		}
		v.ExpiredAt = time.Unix(sub.ExpiresAt, 0).In(s.Location()).Format("2006-01-02 15:04")
		if p, err := s.Queries.GetPlan(ctx, sub.PlanID); err == nil {
			v.PlanName, v.Amount = p.Name, view.Money(p.Price)
		}
		break
	}
	return v
}

// isolirCustomer finds the customer of a client IP: the owner of the most recent open RADIUS session
// on that framed IP. ok is false when there is none.
func (s *Handlers) isolirCustomer(ctx context.Context, ip string) (c db.Customer, ok bool) {
	sess, err := s.Queries.GetOpenRadiusSessionByFramedIP(ctx, ip)
	if err != nil {
		return c, false
	}
	c, err = s.Queries.GetCustomerByUsername(ctx, sess.Username)
	return c, err == nil
}

// isolirIsolated reports whether the customer has an isolation that is running now.
func (s *Handlers) isolirIsolated(ctx context.Context, customerID int64) bool {
	st, err := s.LoadSettings(ctx)
	if err != nil {
		return false
	}
	return isolir.Active(ctx, s.Queries, st, customerID, s.Now())
}

// IsolirGate serves the isolir page to an isolated client for GET and HEAD requests, whatever Host
// and path it asked for. It wraps the whole router, so it runs before the root redirect. Paths the
// client needs to pay or log in stay reachable (isolirExempt). With isolir off it does no query.
func (s *Handlers) IsolirGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.IsolirOn() || (r.Method != http.MethodGet && r.Method != http.MethodHead) || isolirExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		c, ok := s.isolirCustomer(r.Context(), core.ClientIP(r))
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
func (s *Handlers) writeIsolir(w http.ResponseWriter, r *http.Request, st map[string]string, v isolirView, status int, notice ...string) {
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
	v.Promos = s.PromoBlocks(r.Context(), "isolir")
	v.WAAdmin = isolirWALink(v, isolir.Value(st, "isolir_wa"))
	v.LogoutURL = isolirLogoutURL(st)
	v.PortalURL = isolirPortalURL(st)

	custom := isolir.Value(st, "isolir_mode") == "custom" && strings.TrimSpace(st["isolir_html"]) != ""
	s.emitIsolir(w, st, v, status, custom, st["isolir_html"])
}

func (s *Handlers) emitIsolir(w http.ResponseWriter, st map[string]string, v isolirView, status int, custom bool, src string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if custom {
		w.Header().Set("Content-Security-Policy", isolirCSP)
		w.WriteHeader(status)
		w.Write([]byte(renderIsolirCustom(src, v)))
		return
	}
	w.WriteHeader(status)
	if err := s.isolirDefault().Execute(w, v); err != nil {
		s.Fail(w, "isolir page", err)
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
	link := view.WaLink(phone, "62")
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
		"wa_admin_link": v.WAAdmin, "portal_url": v.PortalURL, "voucher_form": string(v.VoucherForm), "promos": string(v.Promos),
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
.btn{display:block;box-sizing:border-box;width:100%;text-align:center;background:#15803d;color:#fff;text-decoration:none;border:0;border-radius:8px;padding:12px;margin:8px 0;font:600 1rem system-ui,sans-serif;cursor:pointer}
.btn-ghost{background:#fff;color:#18181b;border:1px solid #d4d4d8}
input{display:block;box-sizing:border-box;width:100%;padding:10px;border:1px solid #d4d4d8;border-radius:8px;font:1rem system-ui,sans-serif}
img.logo{display:block;max-height:48px;max-width:180px;margin-bottom:8px}
</style></head><body><main>
{{if .Logo}}<img class="logo" src="{{.Logo}}" alt="{{.Company}}">{{end}}
{{if .Notice}}<div class="card"><p>{{T .Notice}}</p></div>{{end}}
<div class="card"><h1>{{.Title}}</h1>
{{if .Known}}<p>{{T "Hello"}} <b>{{.Name}}</b></p><p class="muted">{{T "Last plan"}}: {{.PlanName}}<br>{{T "Ends"}}: {{.ExpiredAt}}</p>{{end}}
<p>{{.Message}}</p>{{if and .Known .Amount}}<p><b>{{T "Package price"}}: {{.Amount}}</b></p>{{end}}</div>
<a class="btn" href="{{.PortalURL}}" rel="noopener">{{T "Pay / extend package"}}</a>
{{if .WAAdmin}}<a class="btn" href="{{.WAAdmin}}" rel="noopener">{{T "Contact admin"}}</a>{{end}}
{{if .LogoutURL}}<a class="btn btn-ghost" href="{{.LogoutURL}}" rel="noopener">{{T "Log out / use another account"}}</a>{{end}}
{{if .ShowVoucher}}<div class="card"><h2>{{T "Have a voucher?"}}</h2>{{.VoucherForm}}</div>{{end}}
{{.Promos}}
</main></body></html>`

// isolirDefault parses the default layout with T bound to the request language.
func (s *Handlers) isolirDefault() *template.Template {
	return template.Must(template.New("isolir").Funcs(template.FuncMap{"T": func(text string) string { return s.T(text) }}).Parse(isolirDefaultSrc))
}

// IsolirPreview is GET /admin/settings/isolir/preview: the page with sample data, in a new tab. The
// custom HTML is used when it is not blank, so a draft can be checked after saving.
func (s *Handlers) IsolirPreview(w http.ResponseWriter, r *http.Request) {
	st, err := s.LoadSettings(r.Context())
	if err != nil {
		s.Fail(w, "isolir settings", err)
		return
	}
	v := isolirView{Known: true, CustomerID: 0, Name: "Budi", Username: "budi", PlanName: "Plan 10 Mbps",
		ExpiredAt: "2026-01-31 23:59", Amount: view.Money(150000), ShowVoucher: true}
	v.Title = isolir.Value(st, "isolir_title")
	v.Message = isolir.Value(st, "isolir_message")
	v.Company = st["company_name"]
	if st["logo"] != "" {
		v.Logo = "/uploads/" + st["logo"]
	}
	v.VoucherForm = s.isolirVoucherForm()
	v.Promos = s.PromoBlocks(r.Context(), "isolir")
	v.WAAdmin = isolirWALink(v, isolir.Value(st, "isolir_wa"))
	v.LogoutURL = isolirLogoutURL(st)
	v.PortalURL = isolirPortalURL(st)
	v.Notice = "Sample view. Customer data is not real."
	s.emitIsolir(w, st, v, http.StatusOK, strings.TrimSpace(st["isolir_html"]) != "", st["isolir_html"])
}

// isolirPortalURL is where an isolated customer pays: the login page, on the app URL when it is set.
func isolirPortalURL(st map[string]string) string {
	return strings.TrimRight(st["app_url"], "/") + "/login"
}
