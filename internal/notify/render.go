package notify

import (
	"regexp"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

// Default templates, from notifications.default.json (override with settings notif_<name>).
var defaults = map[string]string{
	"expired":          "Hello [[name]], your internet package [[package]] has been expired.",
	"reminder_7_day":   "Hello *[[name]]*, \r\nyour internet package *[[package]]* will be expired in 7 days.",
	"reminder_3_day":   "Hello *[[name]]*, \r\nyour internet package *[[package]]* will be expired in 3 days.",
	"reminder_1_day":   "Hello *[[name]]*,\r\n your internet package *[[package]]* will be expired tomorrow.",
	"balance_send":     "You sent [[balance]] to [[name]].",
	"balance_received": "You have received [[balance]] from [[name]].",
	"welcome_message":  "Welcome aboard, [[name]]! \r\nWe're excited to have you as a new [[company]] customer. \r\nPortal: [[url]]\r\nYour login is [[Username]]\r\nWelcome to the [[company]] family!",
	"invoice_balance":  "*[[company_name]]*\r\n[[address]]\r\n[[phone]]\r\n\r\n\r\nINVOICE: *[[invoice]]*\r\nDate : [[date]]\r\n[[payment_gateway]] [[payment_channel]]\r\n\r\n\r\nType : *[[type]]*\r\nPackage : *[[plan_name]]*\r\nPrice : *[[plan_price]]*\r\n\r\n====================\r\n[[footer]]",
	"invoice_paid":     "*[[company_name]]*\r\n[[address]]\r\n[[phone]]\r\n\r\nINVOICE: *[[invoice]]*\r\nDate : [[date]]\r\n[[payment_gateway]] [[payment_channel]]\r\n\r\nType : *[[type]]*\r\nPackage : *[[plan_name]]*\r\nPrice : *[[plan_price]]*\r\n\r\nUsername : *[[user_name]]*\r\n\r\nExpired : *[[expired_date]]*\r\n\r\n====================\r\n[[footer]]",
}

// DefaultTemplate is the built-in message for name, shown in the settings form while empty.
func DefaultTemplate(name string) string { return defaults[name] }

// Render replaces [[key]] placeholders; unknown ones stay as-is (str_replace).
func Render(tpl string, vars map[string]string) string {
	kv := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		kv = append(kv, "[["+k+"]]", v)
	}
	return strings.NewReplacer(kv...).Replace(tpl)
}

var anyPlaceholder = regexp.MustCompile(`\[\[[^\[\]]*\]\]`)

// fill is Render for messages that get sent: payment_link / invoice_link become absolute
// with app_url (empty without it), and a placeholder nobody filled becomes "" instead of
// reaching the customer as raw "[[price]]".
func (n *Notifier) fill(tpl string, v map[string]string) string {
	out := make(map[string]string, len(v)+2)
	for k, x := range v {
		out[k] = x
	}
	if _, ok := out["payment_link"]; !ok {
		out["payment_link"] = "/portal/plans"
	}
	base := strings.TrimRight(n.get("app_url"), "/")
	for _, k := range []string{"payment_link", "invoice_link"} {
		if strings.HasPrefix(out[k], "/") {
			if base == "" {
				out[k] = ""
			} else {
				out[k] = base + out[k]
			}
		}
	}
	msg := anyPlaceholder.ReplaceAllString(Render(tpl, out), "")
	if out["qris_link"] != "" && !strings.Contains(tpl, "[[qris_link]]") {
		msg += "\n\nBayar via QRIS: " + out["qris_link"]
	}
	return msg
}

// Fill is fill for a message to customer c: the customer's name, username and user_name, then vars.
func (n *Notifier) Fill(tpl string, c db.Customer, vars map[string]string) string {
	return n.fill(tpl, custVars(c, vars))
}

// template is the stored text of name, or the built-in one when the stored text is blank: a message is
// never sent empty.
func (n *Notifier) template(name string) string {
	if t := n.get("notif_" + name); strings.TrimSpace(t) != "" {
		return t
	}
	return defaults[name]
}
