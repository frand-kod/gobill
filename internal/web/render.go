package web

// Template parsing, icon loading, page rendering and flash/redirect helpers.

import (
	"html/template"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/view"

	"path"
	"strings"

	gobill "github.com/frand-kod/gobill"
)

func (s *Server) flashTo(w http.ResponseWriter, r *http.Request, to, msg string) {
	s.Sessions.Put(r.Context(), "flash", s.T(msg))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---- templates ----

func (s *Server) parseTemplates() error {
	funcs, err := view.Funcs()
	if err != nil {
		return err
	}
	funcs["T"] = func(text string) string { return s.T(text) }
	// TT translates the part before ": ", so headings like "Edit Contact: budi" work.
	funcs["TT"] = func(text string) string {
		if head, rest, ok := strings.Cut(text, ": "); ok {
			return s.T(head) + ": " + rest
		}
		return s.T(text)
	}
	funcs["asset"] = s.asset
	funcs["ts"] = s.TS
	pages := map[string][]string{
		"login":            {"base.html", "login.html"},
		"login_2fa":        {"base.html", "login_2fa.html"},
		"admin_2fa":        {"base.html", "admin/app.html", "admin/admin_2fa.html"},
		"dashboard":        {"base.html", "admin/app.html", "admin/dashboard.html", "barchart.html"},
		"status":           {"base.html", "admin/app.html", "admin/status.html", "sparkline.html"},
		"list":             {"base.html", "admin/app.html", "admin/list.html"},
		"form":             {"base.html", "admin/app.html", "admin/form.html", "admin/formfields.html", "admin/customer_pick.html", "admin/tplvars.html"},
		"customer":         {"base.html", "admin/app.html", "admin/customer.html", "admin/radius_usage.html", "admin/recharge_dialog.html", "barchart.html"},
		"radius_sessions":  {"base.html", "admin/app.html", "admin/radius_sessions.html"},
		"print":            {"admin/print.html"},
		"voucher_view":     {"base.html", "admin/app.html", "admin/voucher_view.html"},
		"recharge_confirm": {"base.html", "admin/app.html", "admin/recharge_confirm.html", "admin/recharge_dialog.html"},
		"deposit_confirm":  {"base.html", "admin/app.html", "admin/deposit_confirm.html"},
		"recharge":         {"base.html", "admin/app.html", "admin/recharge.html", "admin/customer_pick.html", "admin/customer_summary.html", "admin/recharge_dialog.html"},
		"report":           {"base.html", "admin/app.html", "admin/report.html", "barchart.html"},
		"report_print":     {"admin/report_print.html"},
		"invoice":          {"admin/invoice.html"},
		"maps":             {"base.html", "admin/app.html", "admin/maps.html"},
		"pay_audit":        {"base.html", "admin/app.html", "admin/pay_audit.html"},
		"docs":             {"base.html", "admin/app.html", "admin/docs.html"},
		"network":          {"base.html", "admin/app.html", "admin/network.html"},
		"import":           {"base.html", "admin/app.html", "admin/import.html"},
		"restore":          {"base.html", "admin/app.html", "admin/restore.html"},
		"apiaccess":        {"base.html", "admin/app.html", "admin/apiaccess.html"},
		"key":              {"base.html", "admin/app.html", "admin/key.html"},
		"search":           {"base.html", "admin/app.html", "admin/search.html"},

		"p_login":        {"base.html", "portal/login.html"},
		"p_qris":         {"base.html", "portal/qris.html"},
		"p_register":     {"base.html", "portal/register.html"},
		"p_dashboard":    {"base.html", "portal/layout.html", "portal/dashboard.html", "portal/dash_cards.html", "barchart.html", "promo_cards.html"},
		"p_connection":   {"base.html", "portal/layout.html", "portal/connection.html"},
		"p_profile":      {"base.html", "portal/layout.html", "portal/profile.html"},
		"p_orders":       {"base.html", "portal/layout.html", "portal/orders.html"},
		"p_plans":        {"base.html", "portal/layout.html", "portal/plans.html"},
		"p_inbox":        {"base.html", "portal/layout.html", "portal/inbox.html"},
		"p_voucher":      {"base.html", "portal/layout.html", "portal/voucher.html"},
		"p_forgot":       {"base.html", "portal/forgot.html"},
		"p_page":         {"base.html", "portal/page.html"},
		"p_payment":      {"base.html", "portal/layout.html", "portal/payment.html"},
		"p_activation":   {"base.html", "portal/layout.html", "portal/activation.html"},
		"p_friend":       {"base.html", "portal/layout.html", "portal/friend.html"},
		"p_forgot_user":  {"base.html", "portal/forgot_user.html"},
		"bulk_recharge":  {"base.html", "admin/app.html", "admin/bulk_recharge.html"},
		"admin_password": {"base.html", "admin/app.html", "admin/admin_password.html"},
		"setup":          {"base.html", "admin/app.html", "admin/setup.html", "admin/formfields.html", "admin/customer_pick.html", "admin/tplvars.html"},
	}
	s.Templates = map[string]*template.Template{}
	for name, files := range pages {
		for i, f := range files {
			files[i] = path.Join("web/templates", f)
		}
		t, err := template.New("").Funcs(funcs).ParseFS(gobill.FS, files...)
		if err != nil {
			return err
		}
		s.Templates[name] = t
	}
	return nil
}
