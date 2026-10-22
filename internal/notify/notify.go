// Package notify ports PHPNuxBill's Message.php: Telegram, URL-gateway SMS/WhatsApp, SMTP email
// and an outgoing signed webhook (replacing the old plugin hooks). Setting keys keep the old
// appconfig names. Everything is a no-op when its config is empty. Use Go to send without
// blocking billing.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/metrics"
	mail "github.com/wneessen/go-mail"
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

type Notifier struct {
	Settings    map[string]string
	HTTP        *http.Client
	TelegramAPI string // default https://api.telegram.org
	Timeout     time.Duration
	// Log, if set, is called after every send attempt (channel: telegram, sms, wa, email);
	// it records the attempt without notify knowing about the database.
	Log func(channel, recipient, subject, body string, err error)
}

func (n *Notifier) logged(ch, to, subject, body string, err error) error {
	err = clean(err)
	if n.Log != nil {
		n.Log(ch, to, subject, body, err)
	}
	record(ch, err)
	return err
}

// ChannelState is the send health of one channel since start, for the status page and alerts.
type ChannelState struct {
	Name         string
	Streak       int // consecutive failures; 0 after any success
	LastErr      string
	LastAt       time.Time
	Sent, Failed float64
}

var chans = struct {
	sync.Mutex
	m map[string]*ChannelState
}{m: map[string]*ChannelState{}}

// record counts one send attempt on channel ch in the metrics and keeps its streak and last error.
func record(ch string, err error) {
	chans.Lock()
	defer chans.Unlock()
	s := chans.m[ch]
	if s == nil {
		s = &ChannelState{Name: ch}
		chans.m[ch] = s
	}
	s.LastAt = time.Now()
	if err == nil {
		s.Streak = 0
		metrics.Inc("notifications_sent_total", "channel", ch)
		return
	}
	s.Streak++
	s.LastErr = scrub(err.Error())
	metrics.Inc("notifications_failed_total", "channel", ch)
}

// Channels returns the state of every channel that has been used since start.
func Channels() []ChannelState {
	chans.Lock()
	defer chans.Unlock()
	out := make([]ChannelState, 0, len(chans.m))
	for _, s := range chans.m {
		c := *s
		c.Sent = metrics.Value("notifications_sent_total", "channel", s.Name)
		c.Failed = metrics.Value("notifications_failed_total", "channel", s.Name)
		out = append(out, c)
	}
	return out
}

var (
	scrubURL   = regexp.MustCompile(`https?://\S+`)
	scrubToken = regexp.MustCompile(`\d{6,}:[A-Za-z0-9_-]+`)
	scrubPhone = regexp.MustCompile(`\+?\d{7,}`)
)

// Scrub is scrub for the web layer: error text without URLs, bot tokens or phone numbers.
func Scrub(s string) string { return scrub(s) }

// scrub removes URLs, bot tokens and phone-like numbers from error text before it is shown.
func scrub(s string) string {
	s = scrubURL.ReplaceAllString(s, "<url>")
	s = scrubToken.ReplaceAllString(s, "<token>")
	s = scrubPhone.ReplaceAllString(s, "<nomor>")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// clean drops the request URL from transport errors: gateway URLs carry API keys, the bot
// token and the message text.
func clean(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// LogTo returns a Log hook that stores each attempt in message_logs (body cut to 500 runes).
func LogTo(q *db.Queries) func(ch, to, subject, body string, err error) {
	return func(ch, to, subject, body string, err error) {
		status, msg := "ok", ""
		if err != nil {
			status, msg = "error", err.Error()
		}
		if r := []rune(body); len(r) > 500 {
			body = string(r[:500])
		}
		if e := q.CreateMessageLog(context.Background(), db.CreateMessageLogParams{Channel: ch, Recipient: to,
			Subject: subject, Body: body, Status: status, Error: msg}); e != nil {
			slog.Error("message log", "err", e)
		}
	}
}

// Load reads all settings.
func Load(ctx context.Context, q *db.Queries) (*Notifier, error) {
	rows, err := q.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	n := &Notifier{Settings: map[string]string{}, HTTP: &http.Client{Timeout: 20 * time.Second}, TelegramAPI: "https://api.telegram.org", Timeout: 20 * time.Second}
	for _, r := range rows {
		n.Settings[r.Key] = r.Value
	}
	return n, nil
}

// MessageDelay is the pause between bulk messages: setting message_delay in seconds, default 1.
func MessageDelay(st map[string]string) time.Duration {
	sec, err := strconv.ParseFloat(st["message_delay"], 64)
	if err != nil || sec < 0 {
		sec = 1
	}
	return time.Duration(sec * float64(time.Second))
}

func (n *Notifier) get(k string) string { return n.Settings[k] }

// DefaultTemplate is the built-in message for name, shown in the settings form while empty.
func DefaultTemplate(name string) string { return defaults[name] }

// Go runs fn in a goroutine with a timeout; errors are only logged.
func (n *Notifier) Go(name string, fn func(context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), n.Timeout)
		defer cancel()
		if err := fn(ctx); err != nil {
			slog.Error("notify failed", "kind", name, "err", err)
		}
	}()
}

// do sends req and returns the HTTP status (0 when there was no answer).
func (n *Notifier) do(ctx context.Context, req *http.Request) (int, error) {
	resp, err := n.HTTP.Do(req.WithContext(ctx))
	if err != nil {
		return 0, clean(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s: HTTP %d", req.URL.Host, resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func (n *Notifier) getURL(ctx context.Context, u string) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return clean(err)
	}
	_, err = n.do(ctx, req)
	return err
}

// Telegram sends text to telegram_target_id (sendTelegram).
func (n *Notifier) Telegram(ctx context.Context, text string) error {
	if n.get("telegram_bot") == "" || text == "" {
		return nil
	}
	return n.logged("telegram", n.get("telegram_target_id"), "", text, n.getURL(ctx, n.TelegramAPI+"/bot"+n.get("telegram_bot")+"/sendMessage?chat_id="+
		url.QueryEscape(n.get("telegram_target_id"))+"&text="+url.QueryEscape(text)))
}

func gateway(tpl, phone, text string) string {
	return strings.NewReplacer("[number]", url.QueryEscape(phone), "[text]", url.QueryEscape(text)).Replace(tpl)
}

// SMS calls the message gateway (GET) with [number]/[text] substituted: sms_url when an old install
// still has it set, otherwise wa_url. ponytail: the old MikroTik-SMS mode (sms_url not starting
// with http) is not ported.
func (n *Notifier) SMS(ctx context.Context, phone, text string) error {
	tpl := n.get("sms_url")
	if tpl == "" {
		tpl = n.get("wa_url")
	}
	if tpl == "" || text == "" {
		return nil
	}
	return n.logged("sms", phone, "", text, n.getURL(ctx, gateway(tpl, n.phoneFormat(nonDigit.ReplaceAllString(phone, "")), text)))
}

// WAError is a failed send through the alt WhatsApp gateway. Status is 0 when the server
// could not be reached; Detail is the server's own message.
type WAError struct {
	Status int
	Detail string
}

func (e *WAError) Error() string {
	if e.Status == 0 {
		return "WA server: " + e.Detail
	}
	return fmt.Sprintf("WA server: HTTP %d: %s", e.Status, e.Detail)
}

// isPHPPlugin reports a wa_url that points at the old PHP plugin route (plugin/wga_sendMessage).
func isPHPPlugin(u string) bool { return strings.Contains(u, "wga_sendMessage") }

// WAConfigured reports whether WhatsApp can be sent: alt_wga_server_url, or a usable wa_url.
func WAConfigured(st map[string]string) bool {
	return st["alt_wga_server_url"] != "" || st["wa_url"] != ""
}

// SMSConfigured reports whether SMS can be sent: wa_url, or the legacy sms_url.
func SMSConfigured(st map[string]string) bool {
	return st["sms_url"] != "" || st["wa_url"] != ""
}

var phpPluginWarned sync.Once

// WhatsApp sends through the alt WA server when alt_wga_server_url is set (what the PHP plugin
// wga_sendMessage did), otherwise calls wa_url (GET). The number gets country_code_phone like Lang::phoneFormat.
func (n *Notifier) WhatsApp(ctx context.Context, phone, text string) error {
	if text == "" {
		return nil
	}
	if n.get("alt_wga_server_url") != "" {
		if isPHPPlugin(n.get("wa_url")) {
			phpPluginWarned.Do(func() {
				slog.Warn("wa_url still points at the old PHPNuxBill plugin (wga_sendMessage); ignoring it and sending straight to alt_wga_server_url. Clear wa_url in Settings > Integrations.")
			})
		}
		return n.logged("wa", phone, "", text, n.AltWA(ctx, phone, text))
	}
	if n.get("wa_url") == "" {
		return nil
	}
	return n.logged("wa", phone, "", text, n.getURL(ctx, gateway(n.get("wa_url"), n.phoneFormat(phone), text)))
}

// AltWA POSTs {phone: "<digits>@s.whatsapp.net", message} to <alt_wga_server_url>/send/message,
// as the PHP plugin did (go-whatsapp-web-multidevice REST API): basic auth when username and
// password are set, X-Device-Id when alt_wga_device_id is set. Success is 2xx and, when the
// body has a "code", 200/201/SUCCESS. It does not log to message_logs.
func (n *Notifier) AltWA(ctx context.Context, phone, text string) error {
	num := n.phoneFormat(nonDigit.ReplaceAllString(phone, ""))
	if len(num) < 10 || len(num) > 15 {
		return &WAError{Detail: "invalid phone number"}
	}
	body, _ := json.Marshal(map[string]string{"phone": num + "@s.whatsapp.net", "message": text})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(n.get("alt_wga_server_url"), "/")+"/send/message", bytes.NewReader(body))
	if err != nil {
		return &WAError{Detail: "invalid server URL"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if d := n.get("alt_wga_device_id"); d != "" {
		req.Header.Set("X-Device-Id", d)
	}
	if u, p := n.get("alt_wga_username"), n.get("alt_wga_password"); u != "" && p != "" {
		req.SetBasicAuth(u, p)
	}
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return &WAError{Detail: clean(err).Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var r struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &r)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if c := strings.ToUpper(fmt.Sprint(r.Code)); r.Code != nil && c != "200" && c != "201" && c != "SUCCESS" {
		ok = false
	}
	if ok {
		return nil
	}
	d := r.Message
	if d == "" {
		d = strings.TrimSpace(string(raw))
	}
	if r := []rune(d); len(r) > 200 {
		d = string(r[:200])
	}
	return &WAError{Status: resp.StatusCode, Detail: d}
}

var nonDigit = regexp.MustCompile(`[^0-9]`)

var digits = regexp.MustCompile(`^[0-9]+$`)

func (n *Notifier) phoneFormat(p string) string {
	if cc := n.get("country_code_phone"); cc != "" && digits.MatchString(p) && strings.HasPrefix(p, "0") {
		return cc + p[1:]
	}
	return p
}

// Email sends through smtp_host. Empty smtp_host skips (old code fell back to PHP mail()).
func (n *Notifier) Email(ctx context.Context, to, subject, body string) error {
	if n.get("smtp_host") == "" || to == "" || body == "" {
		return nil
	}
	c, m, err := n.mailer(to, subject, body)
	if err == nil {
		err = c.DialAndSendWithContext(ctx, m)
	}
	return n.logged("email", to, subject, body, err)
}

func (n *Notifier) mailer(to, subject, body string) (*mail.Client, *mail.Msg, error) {
	port, err := strconv.Atoi(n.get("smtp_port"))
	if err != nil || port <= 0 {
		return nil, nil, errors.New("smtp_port invalid")
	}
	if n.get("mail_from") == "" {
		return nil, nil, errors.New("mail_from required")
	}
	opts := []mail.Option{mail.WithPort(port), mail.WithTLSPolicy(mail.TLSOpportunistic)}
	switch n.get("smtp_ssltls") {
	case "ssl":
		opts = append(opts, mail.WithSSL())
	case "tls":
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	}
	if n.get("smtp_user") != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover), mail.WithUsername(n.get("smtp_user")), mail.WithPassword(n.get("smtp_pass")))
	}
	c, err := mail.NewClient(n.get("smtp_host"), opts...)
	if err != nil {
		return nil, nil, err
	}
	m := mail.NewMsg()
	if err := m.From(n.get("mail_from")); err != nil {
		return nil, nil, err
	}
	if err := m.To(to); err != nil {
		return nil, nil, err
	}
	if r := n.get("mail_reply_to"); r != "" {
		_ = m.ReplyTo(r)
	}
	m.Subject(subject)
	m.SetBodyString(mail.TypeTextPlain, body)
	return c, m, nil
}

// Webhook POSTs {"event","time","data"} signed with X-Signature: sha256=HMAC(webhook_secret, body).
// Events: customer.activated, recharge.expired, payment.paid.
func (n *Notifier) Webhook(ctx context.Context, event string, payload any) error {
	_, err := n.PostWebhook(ctx, event, payload)
	return err
}

// PostWebhook is Webhook that also returns the HTTP status (0 when there is no URL or no answer).
func (n *Notifier) PostWebhook(ctx context.Context, event string, payload any) (int, error) {
	u := n.get("webhook_url")
	if u == "" {
		return 0, nil
	}
	body, err := json.Marshal(map[string]any{"event": event, "time": time.Now().Unix(), "data": payload})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event", event)
	mac := hmac.New(sha256.New, []byte(n.get("webhook_secret")))
	mac.Write(body)
	req.Header.Set("X-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	status, err := n.do(ctx, req)
	record("webhook", err)
	return status, err
}

// Render replaces [[key]] placeholders; unknown ones stay as-is (str_replace).
func Render(tpl string, vars map[string]string) string {
	kv := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		kv = append(kv, "[["+k+"]]", v)
	}
	return strings.NewReplacer(kv...).Replace(tpl)
}

var anyPlaceholder = regexp.MustCompile(`\[\[[^\[\]]*\]\]`)

// Money formats rupiah like the old Lang::moneyFormat: "Rp 1.234.000".
func Money(v int64) string {
	d, neg := strconv.FormatInt(v, 10), ""
	if v < 0 {
		d, neg = d[1:], "-"
	}
	for i := len(d) - 3; i > 0; i -= 3 {
		d = d[:i] + "." + d[i:]
	}
	return "Rp " + neg + d
}

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

// template is the stored text of name, or the built-in one when the stored text is blank: a message is
// never sent empty.
func (n *Notifier) template(name string) string {
	if t := n.get("notif_" + name); strings.TrimSpace(t) != "" {
		return t
	}
	return defaults[name]
}

// CustomersOff is the notify_customers switch: "no" stops every customer message. Missing means on.
func CustomersOff(st map[string]string) bool { return st["notify_customers"] == "no" }

// send picks the channel like sendPackageNotification: phone must be > 5 chars for sms/wa.
func (n *Notifier) send(ctx context.Context, c db.Customer, via, subject, msg string) error {
	if CustomersOff(n.Settings) {
		slog.Info("customer notification off", "kind", subject)
		return nil
	}
	switch via {
	case "sms", "wa":
		if len(c.Phone) <= 5 {
			return nil
		}
		if via == "sms" {
			return n.SMS(ctx, c.Phone, msg)
		}
		return n.WhatsApp(ctx, c.Phone, msg)
	case "email":
		return n.Email(ctx, c.Email, "["+n.get("company_name")+"] "+subject, msg)
	}
	slog.Warn("customer notification has no channel", "kind", subject, "via", via, "customer", c.Username)
	return nil
}

func custVars(c db.Customer, v map[string]string) map[string]string {
	out := map[string]string{"name": c.Fullname, "username": c.Username, "user_name": c.Username}
	for k, x := range v {
		out[k] = x
	}
	return out
}

// RechargeSuccess sends the invoice_paid message via user_notification_payment (Package.php).
// vars: invoice, date, payment_gateway, payment_channel, type, plan_name, plan_price, expired_date, ...
func (n *Notifier) RechargeSuccess(ctx context.Context, c db.Customer, vars map[string]string) error {
	return n.invoice(ctx, c, "invoice_paid", vars)
}

// RechargeBalance sends the invoice_balance message for a balance top-up (Package.php #rechargeBalance),
// in place of the paid invoice. vars are the RechargeSuccess ones plus balance_before and balance.
func (n *Notifier) RechargeBalance(ctx context.Context, c db.Customer, vars map[string]string) error {
	return n.invoice(ctx, c, "invoice_balance", vars)
}

func (n *Notifier) invoice(ctx context.Context, c db.Customer, name string, vars map[string]string) error {
	v := custVars(c, vars)
	for k, s := range map[string]string{"company_name": "company_name", "address": "address", "phone": "phone", "footer": "note"} {
		if _, ok := v[k]; !ok {
			v[k] = n.get(s)
		}
	}
	return n.send(ctx, c, n.get("user_notification_payment"), "Invoice #"+v["invoice"], n.fill(n.template(name), v))
}

// Expired sends the expired message via user_notification_expired (cron.php).
func (n *Notifier) Expired(ctx context.Context, c db.Customer, pkg string, vars map[string]string) error {
	v := custVars(c, vars)
	v["package"], v["plan"] = pkg, pkg
	return n.send(ctx, c, n.get("user_notification_expired"), "Internet Plan Expired", n.fill(n.template("expired"), v))
}

// Custom sends template `name` (settings notif_<name>) via user_notification_payment.
func (n *Notifier) Custom(ctx context.Context, c db.Customer, name, subject string, vars map[string]string) error {
	return n.send(ctx, c, n.get("user_notification_payment"), subject, n.fill(n.template(name), custVars(c, vars)))
}

// CustomOn is Custom on explicit channels (sms, wa, email) instead of user_notification_payment.
// A real Password (not the "********" mask) that the template has no [[Password]] for goes on its
// own line, so it is not silently dropped.
func (n *Notifier) CustomOn(ctx context.Context, c db.Customer, vias []string, name, subject string, vars map[string]string) error {
	tpl := n.template(name)
	msg, err := n.fill(tpl, custVars(c, vars)), error(nil)
	if pw := vars["Password"]; pw != "" && pw != "********" && !strings.Contains(tpl, "[[Password]]") {
		msg += "\nPassword: " + pw
	}
	for _, v := range vias {
		err = errors.Join(err, n.send(ctx, c, v, subject, msg))
	}
	return err
}

// Reminder sends the 7/3/1-day message via user_notification_reminder (cron_reminder.php);
// notification_reminder_<N>day == "no" disables that day.
func (n *Notifier) Reminder(ctx context.Context, c db.Customer, days int, pkg string, vars map[string]string) error {
	if n.get(fmt.Sprintf("notification_reminder_%dday", days)) == "no" {
		return nil
	}
	v := custVars(c, vars)
	v["package"], v["plan"] = pkg, pkg
	return n.send(ctx, c, n.get("user_notification_reminder"), "Internet Plan Reminder", n.fill(n.template(fmt.Sprintf("reminder_%d_day", days)), v))
}
