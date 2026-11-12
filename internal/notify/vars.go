package notify

import ()

// Var is a [[name]] a message template can use, with a short English description for the settings page.
type Var struct{ Name, Desc string }

// customerVars are the fields every customer message gets (custVars), and payment_link, which fill adds to all.
var customerVars = []Var{
	{"name", "Customer's full name (balance messages: the other customer)"},
	{"username", "Customer's username"},
	{"user_name", "Same as username"},
	{"payment_link", "Link to the plans page in the customer portal"},
}

// packageVars are the price and unpaid bills that sendPackageNotification adds (packageVars).
var packageVars = []Var{
	{"package", "Package name"},
	{"plan", "Same as package"},
	{"price", "Package price"},
	{"bills", "Unpaid bills and the total"},
	{"expired_date", "Expiry date and time"},
}

// Vars lists the variables each message template fills, as the senders pass them (billing and web).
// The settings page shows them under the text box. A template not listed here has no variables.
var Vars = map[string][]Var{
	"expired":          join(customerVars, packageVars),
	"reminder_7_day":   join(customerVars, packageVars),
	"reminder_3_day":   join(customerVars, packageVars),
	"reminder_1_day":   join(customerVars, packageVars),
	"balance_send":     join(customerVars, balanceVars),
	"balance_received": join(customerVars, balanceVars),
	"welcome_message": join(customerVars, []Var{
		{"company", "Company name"},
		{"Username", "Customer's username, as the welcome message names it"},
		{"url", "App URL (Application URL setting)"},
		{"Password", "Password, or ******** when it is not sent"},
	}),
	"invoice_paid": join(customerVars, invoiceVars),
	"invoice_balance": join(customerVars, invoiceVars, []Var{
		{"balance_before", "Balance before the top-up"},
		{"balance", "Balance after the top-up"},
	}),
}

func join(parts ...[]Var) []Var {
	var out []Var
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var balanceVars = []Var{
	{"balance", "Amount transferred"},
	{"current_balance", "Balance left after the transfer"},
}

var invoiceVars = []Var{
	{"company_name", "Company name"},
	{"address", "Address (Invoice address setting)"},
	{"phone", "Phone number (Phone Number setting)"},
	{"footer", "Invoice footer (Invoice Footer setting)"},
	{"invoice", "Invoice number"},
	{"date", "Payment date and time"},
	{"payment_gateway", "Payment gateway, e.g. Tripay or Admin"},
	{"payment_channel", "Payment channel, e.g. QRIS"},
	{"type", "Transaction type"},
	{"plan_name", "Package name"},
	{"plan_price", "Package price"},
	{"price", "Amount paid"},
	{"expired_date", "Expiry date and time"},
	{"trx_date", "Transaction date and time"},
	{"note", "Transaction note"},
	{"bills", "Unpaid bills and the total"},
	{"invoice_link", "Link to the invoice in the customer portal"},
	{"qris_link", "QRIS payment link, when a static QRIS is set"},
}

// VarsOf returns the variables of template name (the notif_ key without its prefix).
func VarsOf(name string) []Var { return Vars[name] }
