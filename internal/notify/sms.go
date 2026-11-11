package notify

import (
	"context"
	"net/url"
	"strings"
)

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

// SMSConfigured reports whether SMS can be sent: wa_url, or the legacy sms_url.
func SMSConfigured(st map[string]string) bool {
	return st["sms_url"] != "" || st["wa_url"] != ""
}
