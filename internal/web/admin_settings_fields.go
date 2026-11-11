package web

// Settings tabs and the fields of each sub-page.

import (
	"encoding/base64"
	"github.com/frand-kod/gobill/internal/service/settingsave"
	"html/template"
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/notify"
	"github.com/frand-kod/gobill/internal/payment"
	"github.com/skip2/go-qrcode"
	"strings"
)

// settingsTabs are the sub-pages in menu order. Integrations is SuperAdmin only; the rest
// are SuperAdmin and Admin, as in the old settings controller. Payment waits for Tripay.
var settingsTabs = []struct {
	Slug, Label string
	SuperOnly   bool
}{
	{"app", "General", false},
	{"localisation", "Localisation", false},
	{"billing", "Customers & Billing", false},
	{"payment", "Payments", true},
	{"integrations", "Integrations", true},
	{"notifications", "Notifications", false},
	{"isolir", "Isolation", true},
	{"wireguard", "WireGuard", true},
	{"promo", "Promotions", true},
	{"appearance", "Appearance", false},
	{"system", "System & Security", false},
}

// settingsSecret are write-only: never rendered, and an empty post keeps the stored value.
var settingsSecret = db.SecretSettings

// settingsFile are image uploads. The setting holds the stored filename; an empty post keeps it.
var settingsFile = map[string]bool{"logo": true, "logo_dark": true, "login_page_logo": true, "login_page_logo_dark": true, "login_page_favicon": true, "login_page_wallpaper": true}

// settingsSuperField are keys on an Admin-visible tab that only a SuperAdmin sees and saves.
var settingsSuperField = map[string]bool{"radius_rest_allow": true, "radius_rest_secret": true, "trust_proxy": true, "trusted_proxies": true}

// forRole drops the SuperAdmin-only fields for any other role, so they are neither rendered nor saved.
func forRole(fs []field, r *http.Request) []field {
	if adminFrom(r).Role == "SuperAdmin" {
		return fs
	}
	var out []field
	for _, f := range fs {
		if !settingsSuperField[f.Name] {
			out = append(out, f)
		}
	}
	return out
}

// settingsSpeed are the Isolir rates. They are not stored as settings: save writes them to the isolir bandwidth row.
var settingsSpeed = settingsave.Speed

// settingsZones is the timezone select; the server still checks any value with time.LoadLocation.
var settingsZones = []string{"Asia/Jakarta", "Asia/Makassar", "Asia/Jayapura", "Asia/Singapore", "Asia/Kuala_Lumpur",
	"Asia/Bangkok", "Asia/Kolkata", "Asia/Dubai", "Asia/Tokyo", "Asia/Shanghai", "Europe/London", "Europe/Paris",
	"Europe/Berlin", "America/New_York", "America/Chicago", "America/Los_Angeles", "Australia/Sydney", "UTC"}

var (
	settingsYesNo = []option{{"yes", "Yes"}, {"no", "No"}}
	// settingsYesByDefault are the yes/no keys the code treats as on while unset (it tests != "no").
	// Every other yes/no key is off while unset.
	settingsYesByDefault = map[string]bool{"notify_customers": true, "notify_otp": true, "isolir_show_voucher": true,
		"extend_expiry": true, "enable_balance": true, "router_check": true, "update_check": true}
	settingsOnOff    = []option{{"on", "On"}, {"off", "Off"}}
	settingsChannels = []option{{"", "Disabled"}, {"sms", "SMS"}, {"wa", "WhatsApp"}, {"email", "Email"}}
)

// settingsFields builds one sub-page. With nil maps it only names the fields, which is how save finds the keys.
// st is the stored settings: it tells whether a secret or an image is already saved.
func (s *Server) settingsFields(tab string, v, e, st map[string]string) []field {
	sel := func(name, label string, opts ...option) field {
		f := text(name, label, v, e).as("select")
		f.Options = opts
		if f.Value == "" && len(opts) == 2 && opts[0].Value == "yes" { // unset: show what the code does, not the first option
			f.Value = "no"
			if settingsYesByDefault[name] {
				f.Value = "yes"
			}
		}
		return f
	}
	chk := func(name, label string) field {
		f := text(name, label, v, e).as("checkbox")
		f.Checked = v[name] == "yes"
		return f
	}
	area := func(name, label string) field { return text(name, label, v, e).as("textarea") }
	// secrets are never filled from v, so they are never rendered back; a saved one gets a hint
	sec := func(name, label string) field {
		hint := "Leave empty to keep the current secret"
		if st[name] != "" {
			hint = "Saved. Leave empty to keep the current secret"
		}
		return text(name, label, nil, e).as("password").hint(hint)
	}
	// notif shows the built-in message only when the setting was never saved; a cleared one stays empty
	notif := func(name, label string) field {
		f := area(name, label)
		key := strings.TrimPrefix(name, "notif_")
		if _, ok := v[name]; !ok {
			f.Value = notify.DefaultTemplate(key)
		}
		f.Vars = notify.VarsOf(key)
		if notify.DefaultTemplate(key) != "" { // nothing to reset to without a built-in text
			f.Reset = "/admin/settings/notifications/reset/" + key
		}
		return f
	}
	// upl is an image upload. Value is the stored filename, shown with a preview and a remove box.
	upl := func(name, label string) field {
		return field{Name: name, Label: label, Type: "file", Value: v[name], Error: e[name], Hint: "PNG, JPG, WebP or ICO, 2 MB max. Uploading replaces the current image"}
	}
	switch tab {
	case "app":
		// company_footer and currency_code have no consumer in gobill, so they are not in the form.
		// Their stored values stay: save only writes the keys listed here.
		out := section([]field{
			text("company_name", "Company Name", v, e).req().hint("Name of your business. Shown on invoices, messages and the login page."),
			text("app_url", "App URL", v, e).hint("Public address of this app, e.g. https://billing.example.com. Fills itself from the address used at the first admin login; change it here if needed. Used for links in WhatsApp messages (QRIS, invoice)."),
			upl("logo", "Company Logo").hint("Logo in the menu bar. Transparent PNG works best, 2 MB max."),
			upl("logo_dark", "Company Logo (dark mode)").hint("Shown in dark mode. Empty = use the light logo."),
			upl("login_page_favicon", "Favicon"),
		}, "Business identity", "")
		out = append(out, section([]field{
			text("phone", "Phone Number", v, e).hint("Operator phone or WhatsApp. Shown to customers on the portal."),
			area("address", "Address"),
			area("note", "Invoice Footer"),
		}, "Contact & invoice", "")...)
		return out
	case "appearance":
		out := section([]field{
			area("login_page_description", "Page Description"),
			upl("login_page_wallpaper", "Login Page Wallpaper"),
		}, "Login page appearance", "")
		// the overrides fall back to the main name and logo when empty
		override := section([]field{
			text("login_page_head", "Login page title (empty = business name)", v, e),
			upl("login_page_logo", "Login page logo (empty = main logo)"),
			upl("login_page_logo_dark", "Login page logo (dark mode)").hint("Only used together with the login page logo above."),
		}, "Login page title & logo", "")
		for i := range override {
			override[i].Fold = true // inside the collapsed "Advanced options"
		}
		return append(out, override...)
	case "localisation":
		zones := append([]string(nil), settingsZones...)
		if z := v["timezone"]; z != "" && !contains(zones, z) {
			zones = append(zones, z)
		}
		out := section([]field{
			text("language", "Language", v, e).opts(s.catalog.Languages()...),
			text("timezone", "Timezone", v, e).opts(zones...).req(),
			text("date_format", "Date Format", v, e).opts("Y-m-d", "d-m-Y", "d/m/Y", "d M Y"),
			text("country_code_phone", "Country Code Phone", v, e).hint("Country code for numbers that start with 0, e.g. 62"),
		}, "Regional", "")
		return append(out, section([]field{
			text("dec_point", "Decimal Point", v, e),
			text("thousands_sep", "Thousands Separator", v, e).hint("Default is a dot: Rp 1.234.000"),
			text("reset_day", "Income Reset Date", v, e).as("number").hint("Day of the month, 1-28"),
		}, "Money", "")...)
	case "notifications":
		out := section([]field{
			sel("notify_customers", "Send notifications to customers", settingsYesNo...).hint("No stops every message to customers: reminders, expired, invoices and QRIS payment links, welcome and balance messages, and manual messages. Operator alerts are not affected"),
			sel("notify_otp", "Send OTP codes", settingsYesNo...).hint("Verification codes for registration, forgot password and contact change. When No, these features say codes are unavailable"),
		}, "Global switches", "")
		out = append(out, section([]field{
			sel("alert_channel", "Operator alert channel", option{"telegram", "Telegram"}, option{"wa", "WhatsApp"}, option{"both", "Telegram and WhatsApp"}).hint("Where alerts about this box go: disk, NAS silent, failing jobs or channels, login attempts, backups. Telegram uses the Telegram ID in Integrations"),
			text("alert_wa_to", "Operator alert WhatsApp number", v, e).hint("Empty = the daily summary number. Needs GOWA or the message gateway URL in Integrations"),
			text("alert_nas_silent_minutes", "NAS silent alert (minutes)", v, e).as("number").hint("Alert when a NAS that sent RADIUS packets before sends none for this many minutes. Default 15"),
			text("alert_mass_drop_min", "Mass drop alert (customers)", v, e).as("number").hint("Alert when at least this many customers of one NAS lose their session within the window below, while the NAS itself is not silent. 0 turns it off. Default 5. RADIUS mode only"),
			text("alert_mass_drop_window", "Mass drop window (minutes)", v, e).as("number").hint("Default 30. Customers whose router stopped reporting are counted after the stale timeout (10 minutes or more), so keep this well above that"),
		}, "Operator alerts", "")...)
		out = append(out, section([]field{
			notif("notif_expired", "Expired Notification Message"),
			notif("notif_reminder_7_day", "Reminder Message (7 days)"),
			notif("notif_reminder_3_day", "Reminder Message (3 days)"),
			notif("notif_reminder_1_day", "Reminder Message (1 day)"),
			notif("notif_invoice_paid", "Invoice Notification Payment"),
			notif("notif_invoice_balance", "Balance Notification Payment"),
			notif("notif_welcome_message", "Welcome Message"),
			notif("notif_balance_send", "Send Balance"),
			notif("notif_balance_received", "Received Balance"),
		}, "Message templates", "")...)
		creds := sel("send_credentials_on_create", "Send username & password by WhatsApp when the admin creates an account", settingsYesNo...).
			hint("Helps customers who do not open the portal. The password is sent as plain text to the customer's own number.")
		if v["send_credentials_on_create"] == "" { // unset means no, not the first option
			creds.Value = "no"
		}
		out = append(out, section([]field{creds}, "Welcome message", "")...)
		out = append(out, section([]field{
			sel("user_notification_expired", "Expired Notification", settingsChannels...),
			text("expired_notify_minutes_before", "Send expired message earlier (minutes)", v, e).as("number").hint("The expired message is sent this many minutes before the plan ends, so a customer still online receives it. The plan itself still ends at its time. Default 10. 0 = at the end of the plan"),
			sel("user_notification_payment", "Payment Notification", settingsChannels...),
			sel("user_notification_reminder", "Reminder Notification", settingsChannels...),
		}, "Channels", "")...)
		out = append(out, section([]field{
			sel("notification_reminder_7day", "Send 7-day reminder", settingsYesNo...),
			sel("notification_reminder_3day", "Send 3-day reminder", settingsYesNo...),
			sel("notification_reminder_1day", "Send 1-day reminder", settingsYesNo...),
			text("reminder_hour", "Reminder Hour", v, e).as("number").req().hint("Hour of day, 0-23, when reminders are sent"),
		}, "Reminders", "")...)
		return append(out, section([]field{
			sel("daily_summary_enabled", "Daily summary", settingsYesNo...).hint("Sends a short report to you (the operator) every morning: income yesterday, new customers, subscriptions expiring, routers offline"),
			text("daily_summary_time", "Daily summary time", v, e).as("time").hint("Server time zone, e.g. 07:00"),
			sel("daily_summary_channel", "Daily summary channel", option{"", "Disabled"}, option{"telegram", "Telegram"}, option{"wa", "WhatsApp"}, option{"both", "Telegram and WhatsApp"}).hint("Telegram uses the Telegram ID in Integrations. WhatsApp needs GOWA or the message gateway URL in Integrations"),
			text("daily_summary_wa_to", "Operator WhatsApp number", v, e).hint("Your own number, e.g. 08123456789"),
			{Name: "daily_summary_now", Label: "Send summary now", Type: "dsnow", Value: v["daily_summary_now"], Error: e["daily_summary_now"],
				Hint: "Sends the summary right now to test it. Save the settings first: the saved values are used"},
		}, "Daily summary", "")...)
	case "integrations":
		out := section([]field{
			sec("telegram_bot", "Telegram Bot Token"),
			text("telegram_target_id", "Telegram User/Channel/Group ID", v, e),
			{Name: "telegram_test", Label: "Test Telegram", Type: "test", Value: "/admin/settings/integrations/test/telegram", Btn: "Send Telegram test message",
				Hint: "Save first. The test uses the saved values and sends a short message to the saved ID"},
		}, "Telegram", "")
		out = append(out, section([]field{
			text("wa_url", "Message gateway URL (WhatsApp/SMS)", v, e).hint("Must contain [number] and [text]. Used for every WhatsApp and SMS message when GOWA below is empty"),
			{Name: "gateway_test_phone", Label: "Test gateway", Type: "test", Inp: "tel", Value: "/admin/settings/integrations/test/gateway", Btn: "Send gateway test",
				Hint: "Save first. The test uses the saved message gateway URL. Type the number to send to"},
		}, "SMS & WhatsApp", "")...)
		out = append(out, section([]field{
			{Name: "gowa_info", Label: "WhatsApp via GOWA (go-whatsapp-web-multidevice):", Type: "note", Value: "https://github.com/aldinokemal/go-whatsapp-web-multidevice", Btn: "github.com/aldinokemal/go-whatsapp-web-multidevice"},
			{Name: "other_wa_info", Label: "Other WhatsApp gateways (Fonnte, Wablas, WAHA, …): coming soon. For now, use the message gateway URL above if the gateway supports GET with [number] and [text].", Type: "note"},
			text("alt_wga_server_url", "GOWA URL", v, e).hint("Address of the GOWA server, e.g. http://127.0.0.1:3030. When filled, WhatsApp is sent straight to this server and the message gateway URL above is ignored"),
			text("alt_wga_device_id", "WA device ID", v, e).hint("Optional. Sent as the X-Device-Id header. Leave empty if the server has only one device"),
			text("alt_wga_username", "GOWA username", v, e),
			sec("alt_wga_password", "GOWA password"),
			{Name: "wa_test_phone", Label: "Send test message", Type: "watest", Error: e["wa_test_phone"], Value: v["wa_test_phone"],
				Hint: "Type a phone number and press the button. Uses the values typed above, even if not saved yet. Devices and QR login are managed in the WA server's own page, not here"},
		}, "WhatsApp — GOWA", "")...)
		out = append(out, section([]field{
			text("smtp_host", "SMTP Host", v, e),
			text("smtp_port", "SMTP Port", v, e).as("number").hint("1-65535"),
			text("smtp_user", "SMTP Username", v, e),
			sec("smtp_pass", "SMTP Password"),
			sel("smtp_ssltls", "SMTP Security", option{"", "None"}, option{"ssl", "SSL"}, option{"tls", "TLS"}),
			text("mail_from", "Mail From", v, e),
			text("mail_reply_to", "Mail Reply To", v, e),
			{Name: "email_test_to", Label: "Test email", Type: "test", Inp: "email", Value: "/admin/settings/integrations/test/email", Btn: "Send test email",
				Hint: "Save first. The test uses the saved SMTP settings. Type the address to send to"},
		}, "Email (SMTP)", "")...)
		out = append(out, section([]field{
			text("webhook_url", "Webhook URL", v, e).hint("http or https. Requests are signed with X-Signature"),
			sec("webhook_secret", "Webhook Secret"),
			{Name: "webhook_test", Label: "Test webhook", Type: "test", Value: "/admin/settings/integrations/test/webhook", Btn: "Send test webhook",
				Hint: "Save first. Sends a signed test event (event \"test\") to the saved URL and shows the HTTP status"},
		}, "Webhook", "")...)
		return append(out, section([]field{
			sec("metrics_token", "Token /metrics"),
			{Name: "metrics_new", Label: "Create new token", Type: "formbtn", Value: "/admin/settings/integrations/metrics-token", Btn: "Create new token",
				Hint:    "Replaces the current token. Old scrapers stop working until they get the new one",
				Confirm: "Create a new token? Old scrapers stop working until they get the new one."},
			{Name: "metrics_off", Label: "Disable", Type: "formbtn", Value: "/admin/settings/integrations/metrics-token/disable", Btn: "Disable",
				Hint:    "Clears the token: /metrics returns 404",
				Confirm: "Disable the /metrics token? /metrics will return 404."},
		}, "Prometheus /metrics", "")...)
	case "payment":
		f := sel("payment_gateway", "Payment Gateway", option{"", "Disabled"}, option{"tripay", "Tripay"}, option{"duitku", "Duitku"})
		out := section([]field{f,
			sec("tripay_api_key", "Tripay API Key"),
			sec("tripay_private_key", "Tripay Private Key"),
			text("tripay_merchant_code", "Tripay Merchant Code", v, e),
			sel("tripay_mode", "Tripay Mode", option{"sandbox", "Sandbox"}, option{"production", "Production"}),
			text("tripay_channel", "Default Channel", v, e).hint("Optional channel code, e.g. QRIS"),
			{Name: "tripay_test", Label: "Test connection", Type: "test", Value: "/admin/settings/payment/tripay-test", Btn: "Check connection",
				Hint: "Save first. Checks the saved keys and merchant code with Tripay's channel list"},
		}, "Tripay", "")
		out = append(out, section([]field{
			sec("duitku_api_key", "Duitku API Key"),
			text("duitku_merchant_code", "Duitku Merchant Code", v, e),
			sel("duitku_mode", "Duitku Mode", option{"sandbox", "Sandbox"}, option{"production", "Production"}),
			text("duitku_channel", "Default Channel", v, e).hint("Optional payment method code, e.g. VC"),
			{Name: "duitku_test", Label: "Test connection", Type: "test", Value: "/admin/settings/payment/duitku-test", Btn: "Check connection",
				Hint: "Save first. Checks the saved key and merchant code with Duitku's payment method list"},
		}, "Duitku", "")...)
		qris := field{Name: "qris_payload", Label: "Static QRIS", Type: "qris", Value: v["qris_payload"], Error: e["qris_payload"],
			Hint: "Upload the merchant's static QRIS photo (PNG/JPG). The system only stores its text"}
		if m, n := payment.QRISInfo(v["qris_payload"]); m != "" {
			qris.Hint = s.t("Active") + ": " + m
			if n != "" {
				qris.Hint += " (NMID " + n + ")"
			}
			// the stored code redrawn (no image is kept), so the admin can check it scans
			if png, err := qrcode.Encode(v["qris_payload"], qrcode.Medium, 256); err == nil {
				qris.Img = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
			}
		}
		return append(out, section([]field{qris}, "QRIS", "")...)
	case "isolir":
		out := section([]field{
			sel("isolir_enabled", "Isolation enabled", settingsYesNo...).hint("A RADIUS customer whose plan expires is isolated instead of disconnected, unless the plan has its own expired plan. The router needs setup, see the MikroTik router setup section below. Switching to No ends all running isolations when you save").warn("Turn isolation off? Every customer now isolated is released when you save.", "no"),
			{Name: "isolir_plan_name", Label: "Plan name", Type: "pre", Value: v["isolir_plan_name"],
				Hint: "System plan: not sold on the portal and cannot be given manually. Created the first time Isolation is enabled"},
			text("isolir_rate_down", "Download speed", v, e).hint("Example 64k or 2M. Saved to the bandwidth profile of the Isolation plan"),
			text("isolir_rate_up", "Upload speed", v, e).hint("Example 64k or 2M"),
			text("isolir_days", "Isolation duration (days)", v, e).as("number").hint("Days after the plan expires, then fully disabled. 0 = isolated until paid"),
			{Name: "isolir_reset_paket", Label: "Restore default", Type: "formbtn", Value: "/admin/settings/isolir/reset/paket", Btn: "Restore default",
				Hint: "Isolation off and 30 days. Speed is not changed"},
		}, "Package", "")
		out = append(out, section([]field{
			text("isolir_title", "Title", v, e),
			area("isolir_message", "Message"),
			text("isolir_wa", "Admin WhatsApp number", v, e).hint("Empty = business phone number (General settings)"),
			sel("isolir_show_voucher", "Show voucher form", settingsYesNo...),
			text("isolir_address_list", "MikroTik address-list name", v, e).hint("Letters, digits, dots, dashes or underscores. Sent by RADIUS as Mikrotik-Address-List"),
			text("isolir_hotspot_url", "Hotspot address", v, e).hint("The address customers use for the hotspot login page, e.g. http://fourkeys.net or http://192.168.20.1. Empty = no logout button on the isolir page"),
			{Name: "isolir_reset_halaman", Label: "Restore default", Type: "formbtn", Value: "/admin/settings/isolir/reset/halaman", Btn: "Restore default",
				Hint: "Title, message, voucher and address-list go back to default"},
		}, "Isolir page", "")...)
		out = append(out, section([]field{
			area("isolir_allow_hosts", "Allowed while isolated").hint("Hosts customers can still reach while isolated, one per line, e.g. wa.me or *.bank.example. The default is WhatsApp. After changing it, paste the router commands again"),
		}, "Router allow-list", "")...)
		out = append(out, section([]field{
			sel("isolir_mode", "Display mode", option{"default", "Default"}, option{"custom", "Custom HTML"}).hint("Custom HTML is used when the HTML is not empty"),
			func() field {
				f := area("isolir_html", "Custom HTML")
				f.Hint = "Full HTML. Values are HTML-escaped, except voucher_form and promos. Unknown or empty variables become empty. Shown in a sandbox without gobill cookie access"
				f.Vars = isolirVarList
				return f
			}(),
			{Name: "isolir_preview", Label: "Isolation page preview (sample data):", Type: "note", Value: "/admin/settings/isolir/preview", Btn: "Open preview"},
			{Name: "isolir_reset_html", Label: "Back to default", Type: "formbtn", Value: "/admin/settings/isolir/reset/html", Btn: "Back to default",
				Hint: "Clears the custom HTML"},
			{Name: "isolir_reset_mode", Label: "Restore default", Type: "formbtn", Value: "/admin/settings/isolir/reset/mode", Btn: "Restore default",
				Hint: "Default mode (custom HTML is not deleted)"},
		}, "Display mode", "")...)
		return out
	case "wireguard":
		return section([]field{
			sel("wg_enabled", "WireGuard enabled", settingsYesNo...).hint("Routers behind NAT connect to gobill through an encrypted tunnel built into gobill: RADIUS, CoA and the RouterOS API all go through it. No root and no host package needed. GOBILL_WG=off in the environment overrides this setting"),
			text("wg_port", "Listen port (UDP)", v, e).as("number").hint("Default 51820. If the provider blocks it, use another port such as 443"),
			text("wg_endpoint", "Public endpoint (domain)", v, e).hint("The domain or public IP routers connect to, without a port. It must point straight at this server: Cloudflare Tunnel and proxied Cloudflare records do not carry UDP"),
			text("wg_subnet", "Tunnel subnet", v, e).hint("Private range for the tunnel, default 10.99.0.0/24. gobill takes the first address, each router one more"),
			chk("wg_confirm", "I understand that routers with a tunnel must be provisioned again"),
		}, "WireGuard", "")
	case "billing":
		out := section([]field{
			sel("extend_expiry", "Extend Package Expiry", settingsYesNo...),
			sel("admin_extend", "Extend button on subscriptions", option{"staff", "All staff (Admin, Agent, Sales)"}, option{"managers", "SuperAdmin and Admin"}, option{"super", "SuperAdmin only"}, option{"off", "Off"}).hint("Who may add free days to a subscription from the list. Off hides the button"),
			sel("start_on_first_login", "Start On First Login", option{"no", "No"}, option{"yes", "Yes"}).hint("RADIUS plans only: the period starts at the customer's first login. MikroTik plans start at once"),
			sel("enable_balance", "Enable Balance System", settingsYesNo...),
			sel("voucher_format", "Voucher Format (default)", option{"up", "UPPERCASE"}, option{"low", "lowercase"},
				option{"rand", "Random case"}, option{"numbers", "Numbers"}),
		}, "Billing", "")
		waUnique := sel("wa_unique_per_account", "One WhatsApp number per account", settingsYesNo...).
			hint("No (default): several accounts may share one WhatsApp number, for example a family or an RT/RW group. Forgot username then lists every account on that number. Yes: a number belongs to one account only")
		if v["wa_unique_per_account"] == "" { // unset means no, not the first option
			waUnique.Value = "no"
		}
		out = append(out, section([]field{
			chk("man_fields_email", "Mandatory field: Email"),
			chk("man_fields_fname", "Mandatory field: Full Name"),
			chk("man_fields_address", "Mandatory field: Address"),
			sel("disable_registration", "Disable Registration", option{"no", "No"}, option{"yes", "Voucher Only"}, option{"noreg", "No Registration"}),
			sel("sms_otp_registration", "SMS OTP Registration", settingsYesNo...),
			sel("phone_otp_type", "OTP Method", option{"sms", "SMS"}, option{"wa", "WhatsApp"}),
			sel("reg_nofify_admin", "Notify Admin", settingsYesNo...),
			waUnique,
			text("customer_password_min", "Minimum Password Length", v, e).as("number").hint("Minimum 4. Password same as username and common passwords are always rejected."),
		}, "Registration", "")...)
		out = append(out, section([]field{
			sel("disable_voucher", "Disable Voucher", settingsYesNo...),
			text("voucher_redirect", "Redirect URL after Activation", v, e).hint("Optional. http or https, e.g. https://192.168.88.1/status"),
			sel("show_bandwidth_plan", "Show Bandwidth Plan", settingsYesNo...),
		}, "Portal", "")...)
		out = append(out, section([]field{
			sel("extend_expired", "Allow Extend", option{"0", "No"}, option{"1", "Yes"}),
			text("extend_days", "Extend Days", v, e).as("number"),
			area("extend_confirmation", "Confirmation Message"),
		}, "Extend", "")...)
		out = append(out, section([]field{
			sel("allow_balance_transfer", "Allow Transfer", settingsYesNo...),
			text("minimum_transfer", "Minimum Balance Transfer", v, e).as("number"),
			sel("allow_balance_custom", "Allow Balance Custom Amount", settingsYesNo...),
		}, "Balance", "")...)
		out = append(out, section([]field{
			sel("allow_phone_otp", "Phone OTP Required", settingsYesNo...),
			sel("allow_email_otp", "Email OTP Required", settingsYesNo...),
		}, "OTP", "")...)
		return append(out, section([]field{
			sel("default_plan_device", "Default Device for New Plans", option{"", "By plan type"}, option{"MikrotikHotspot", "MikrotikHotspot"},
				option{"MikrotikPppoe", "MikrotikPppoe"}, option{"Dummy", "Dummy"}, option{"Radius", "Radius"}).hint("Preselected on the new plan form"),
		}, "Plans", "")...)
	case "system":
		out := section([]field{
			text("session_timeout_duration", "Timeout Duration", v, e).as("number").hint("Minutes of inactivity before an admin is logged out. Empty = 120."),
			sel("single_session", "Single Admin Session", settingsYesNo...),
		}, "Session", "")
		out = append(out, section([]field{
			chk("maintenance_mode", "Maintenance Mode"),
			chk("maintenance_mode_logout", "Log Out Customers During Maintenance"),
			text("maintenance_date", "Maintenance Date", v, e).as("date"),
			sel("clock_guard", "Clock Guard", settingsOnOff...),
			sel("trust_proxy", "Trust Proxy", settingsYesNo...).hint("Honour X-Forwarded-For only from loopback or Trusted Proxies"),
			text("trusted_proxies", "Trusted Proxies", v, e).hint("Comma-separated IPs or CIDRs of your reverse proxies, e.g. 172.16.0.0/16"),
			sel("router_check", "Router Check", settingsYesNo...).hint("Pings enabled routers every 5 minutes and alerts when one goes down"),
			sel("check_customer_online", "Check Customer Online", option{"no", "No"}, option{"yes", "Yes"}),
			sel("update_check", "Check for updates", settingsYesNo...).hint("Once a day, asks GitHub for a newer gobill release. SuperAdmin sees a badge when one exists"),
		}, "System", "")...)
		out = append(out, section([]field{
			text("log_keep_days", "Keep logs (days)", v, e).as("number").hint("Activity logs, closed RADIUS sessions, read messages and unpaid payments older than this are deleted once a day. Default 90. 0 = keep forever. Paid payments are never deleted"),
			text("backup_keep", "Backups kept", v, e).as("number").hint("Number of automatic daily backups kept, in the main folder and in the mirror. Default 7, at least 1"),
		}, "Data retention", "")...)
		out = append(out, section([]field{
			text("radius_rest_allow", "Allowed addresses for /radius.php", v, e).hint("Comma-separated IPs or CIDR ranges that may call /radius.php, e.g. 192.168.1.10, 10.0.0.0/24. Empty = nobody, unless the shared secret below is set. Saved changes apply to the next request, no restart needed"),
			sec("radius_rest_secret", "Shared secret for /radius.php"),
			text("radius_interim_interval", "Accounting update interval (seconds)", v, e).as("number").hint("Sent to the NAS in every Access-Accept as Acct-Interim-Interval. Default 300. 0 = do not send, the NAS uses its own"),
		}, "FreeRADIUS", "")...)
		out = append(out, section([]field{
			sel("role_access_mode", "Role access", option{"strict", "Strict"}, option{"phpnuxbill", "PHPNuxBill compatible"}).hint("Strict: only SuperAdmin and Admin can add balance, use Recharge Zero and generate vouchers, and the Report role sees reports only. PHPNuxBill compatible: Agent and Sales can add balance, use Recharge Zero and generate vouchers, and every role reads transactions and customer exports. A change applies immediately on save").warn("Switch to PHPNuxBill compatible? Agent and Sales can then add balance, use Recharge Zero and generate vouchers, and every role can read transactions and customer exports. This applies immediately.", "phpnuxbill"),
		}, "Access", "")...)
		out = append(out, section([]field{
			sel("hs_auth_method", "Hotspot Auth Method", option{"pap", "PAP"}, option{"chap", "CHAP"}),
			sel("shared_users_policy", "When the device limit is full", option{"kick_oldest", "Kick oldest device"}, option{"reject", "Reject new login"}).hint("Phones now use random MACs. Kick oldest device stops a customer from being locked out when their phone reconnects with a new MAC."),
			sel("shared_users_pppoe", "Apply the device limit to PPPoE", settingsYesNo...).hint("No (default): the Shared Users limit is checked for Hotspot plans only. Yes: PPPoE logins are counted too"),
		}, "Hotspot", "")...)
		return out
	}
	return nil
}

func fieldNames(fs []field) []string {
	var out []string
	for _, f := range fs {
		if f.Type == "watest" || f.Type == "dsnow" || f.Type == "formbtn" || f.Type == "test" || f.Type == "note" || f.Type == "pre" {
			continue // action button or text, not a setting
		}
		out = append(out, f.Name)
	}
	return out
}
