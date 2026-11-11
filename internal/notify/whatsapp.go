package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

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
	return n.logged("wa", phone, "", text, n.getURL(ctx, gateway(n.get("wa_url"), n.phoneFormat(nonDigit.ReplaceAllString(phone, "")), text)))
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
