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
	"invoice_paid":     "*[[company_name]]*\r\n[[address]]\r\n[[phone]]\r\n\r\nINVOICE: *[[invoice]]*\r\nDate : [[date]]\r\n[[payment_gateway]] [[payment_channel]]\r\n\r\nType : *[[type]]*\r\nPackage : *[[plan_name]]*\r\nPrice : *[[plan_price]]*\r\n\r\nUsername : *[[user_name]]*\r\n\r\nExpired : *[[expired_date]]*\r\n\r\n====================\r\n[[footer]]",
}

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
	return err
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
	n := &Notifier{Settings: map[string]string{}, HTTP: http.DefaultClient, TelegramAPI: "https://api.telegram.org", Timeout: 20 * time.Second}
	for _, r := range rows {
		n.Settings[r.Key] = r.Value
	}
	return n, nil
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

func (n *Notifier) do(ctx context.Context, req *http.Request) error {
	resp, err := n.HTTP.Do(req.WithContext(ctx))
	if err != nil {
		return clean(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", req.URL.Host, resp.StatusCode)
	}
	return nil
}

func (n *Notifier) getURL(ctx context.Context, u string) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return clean(err)
	}
	return n.do(ctx, req)
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

// SMS calls sms_url (GET) with [number]/[text] substituted. ponytail: the old MikroTik-SMS
// mode (sms_url not starting with http) is not ported.
func (n *Notifier) SMS(ctx context.Context, phone, text string) error {
	if n.get("sms_url") == "" || text == "" {
		return nil
	}
	return n.logged("sms", phone, "", text, n.getURL(ctx, gateway(n.get("sms_url"), phone, text)))
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
	u := n.get("webhook_url")
	if u == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{"event": event, "time": time.Now().Unix(), "data": payload})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event", event)
	mac := hmac.New(sha256.New, []byte(n.get("webhook_secret")))
	mac.Write(body)
	req.Header.Set("X-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return n.do(ctx, req)
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
	return anyPlaceholder.ReplaceAllString(Render(tpl, out), "")
}

func (n *Notifier) template(name string) string {
	if t := n.get("notif_" + name); t != "" {
		return t
	}
	return defaults[name]
}

// send picks the channel like sendPackageNotification: phone must be > 5 chars for sms/wa.
func (n *Notifier) send(ctx context.Context, c db.Customer, via, subject, msg string) error {
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
	v := custVars(c, vars)
	for k, s := range map[string]string{"company_name": "company_name", "address": "address", "phone": "phone", "footer": "note"} {
		if _, ok := v[k]; !ok {
			v[k] = n.get(s)
		}
	}
	return n.send(ctx, c, n.get("user_notification_payment"), "Invoice #"+v["invoice"], n.fill(n.template("invoice_paid"), v))
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
func (n *Notifier) CustomOn(ctx context.Context, c db.Customer, vias []string, name, subject string, vars map[string]string) error {
	msg, err := n.fill(n.template(name), custVars(c, vars)), error(nil)
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
