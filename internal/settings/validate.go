package settings

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/payment"
)

func oneOf(v string, list ...string) bool { return slices.Contains(list, v) }

var hostRe = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// HostLines splits an allow-list into lowercase hosts, one per line. Blank lines are dropped.
func HostLines(v string) []string {
	var out []string
	for _, line := range strings.Split(v, "\n") {
		if h := strings.ToLower(strings.TrimSpace(line)); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// ValidHost reports whether h (lowercase) is a hostname or a *. wildcard of one, such as wa.me or
// *.whatsapp.net.
func ValidHost(h string) bool { return hostRe.MatchString(h) }

// ParseSubnet accepts a private IPv4 range from /16 to /29, so there are addresses for gobill and a router.
func ParseSubnet(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() || p.Bits() < 16 || p.Bits() > 29 || !p.Addr().IsPrivate() {
		return p, errors.New("want a private IPv4 range from /16 to /29, e.g. 10.99.0.0/24")
	}
	return p.Masked(), nil
}

var rateRe = regexp.MustCompile(`^([1-9][0-9]{0,6})\s*([kKmM]?)$`)

// ParseRate reads "64k" or "2M" as the number and the unit the bandwidth table stores.
func ParseRate(x string) (n int64, unit string, ok bool) {
	m := rateRe.FindStringSubmatch(strings.TrimSpace(x))
	if m == nil {
		return 0, "", false
	}
	n, _ = strconv.ParseInt(m[1], 10, 64)
	if strings.EqualFold(m[2], "m") {
		return n, "Mbps", true
	}
	return n, "Kbps", true
}

// ParseTrustedProxies parses a comma-separated list of IPs and CIDRs. ok is false if any entry is invalid.
func ParseTrustedProxies(spec string) (ps []netip.Prefix, ok bool) {
	ok = true
	for _, e := range strings.Split(spec, ",") {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		if p, err := netip.ParsePrefix(e); err == nil {
			// a catch-all range would let any client spoof X-Forwarded-For
			if p.Bits() < 8 && p.Addr().Is4() || p.Bits() < 32 && !p.Addr().Is4() {
				ok = false
				continue
			}
			ps = append(ps, p)
		} else if a, err := netip.ParseAddr(e); err == nil {
			ps = append(ps, netip.PrefixFrom(a, a.BitLen()))
		} else {
			ok = false
		}
	}
	return ps, ok
}

// wgEndpointRe is a host name or an IPv4 address, without a port.
var wgEndpointRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// validAllowList reports whether every comma-separated entry is an IP or a CIDR range. Empty entries are skipped.
func validAllowList(x string) bool {
	for _, p := range strings.Split(x, ",") {
		p = strings.TrimSpace(p)
		if p == "" || net.ParseIP(p) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(p); err != nil {
			return false
		}
	}
	return true
}

func inRange(x string, lo, hi int) bool {
	n, err := strconv.Atoi(x)
	return err == nil && n >= lo && n <= hi
}

// wgErrors checks the posted WireGuard settings.
func wgErrors(v, e map[string]string) {
	if x, ok := v["wg_port"]; ok && !inRange(x, 1, 65535) {
		e["wg_port"] = "Enter a port between 1 and 65535"
	}
	if x, ok := v["wg_subnet"]; ok {
		if _, err := ParseSubnet(x); err != nil {
			e["wg_subnet"] = "Use a private IPv4 range from /16 to /29, e.g. 10.99.0.0/24"
		}
	}
	if x := v["wg_endpoint"]; x != "" && !wgEndpointRe.MatchString(x) {
		e["wg_endpoint"] = "Enter a domain or an IP address without a port, e.g. vpn.example.com"
	}
}

// Validate checks the posted values of one sub-page. Only keys present in v are checked.
func Validate(v map[string]string, languages []string) map[string]string {
	e := map[string]string{}
	for _, k := range []string{"company_name", "timezone", "reminder_hour"} {
		if x, ok := v[k]; ok && x == "" {
			e[k] = "This field is required"
		}
	}
	if x, ok := v["isolir_days"]; ok {
		if n, err := strconv.Atoi(x); err != nil || n < 0 || n > 3650 {
			e["isolir_days"] = "Enter whole days, 0 to 3650"
		}
	}
	if _, ok := v["wg_port"]; ok {
		wgErrors(v, e)
	}
	if x := v["radius_rest_secret"]; x != "" && len(x) < 16 {
		e["radius_rest_secret"] = "Secret must be at least 16 characters"
	}
	if x, ok := v["isolir_mode"]; ok && x != "default" && x != "custom" {
		e["isolir_mode"] = "Choose one"
	}
	if x, ok := v["isolir_address_list"]; ok && !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`).MatchString(x) {
		e["isolir_address_list"] = "Required: letters, digits, dots, dashes or underscores, max 64 characters"
	}
	if x, ok := v["isolir_allow_hosts"]; ok {
		for _, h := range HostLines(x) {
			if !ValidHost(h) {
				e["isolir_allow_hosts"] = "Each line must be a host such as wa.me or *.example.com (letters, digits, dots and dashes)"
				break
			}
		}
	}
	if x := v["isolir_hotspot_url"]; x != "" {
		if u, err := url.Parse(x); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			e["isolir_hotspot_url"] = "Use an http or https URL"
		}
	}
	if x, ok := v["isolir_html"]; ok && len(x) > 100000 {
		e["isolir_html"] = "Too long (max 100 KB)"
	}
	for _, k := range []string{"isolir_rate_down", "isolir_rate_up"} { // empty keeps the stored rate
		if _, _, valid := ParseRate(v[k]); !valid && v[k] != "" {
			e[k] = "Write a speed such as 64k or 2M"
		}
	}
	if x, ok := v["language"]; ok {
		if !slices.Contains(languages, x) {
			e["language"] = "Choose one of the listed languages"
		}
	}
	if x, ok := v["timezone"]; ok && x != "" {
		if _, err := time.LoadLocation(x); err != nil {
			e["timezone"] = "Unknown timezone. Use a name like Asia/Jakarta."
		}
	}
	if x, ok := v["reminder_hour"]; ok && x != "" && !inRange(x, 0, 23) {
		e["reminder_hour"] = "Enter a whole number from 0 to 23"
	}
	if x := v["daily_summary_time"]; x != "" {
		if _, err := time.Parse("15:04", x); err != nil {
			e["daily_summary_time"] = "Use a time like 07:00"
		}
	}
	if x := v["daily_summary_channel"]; x != "" && !oneOf(x, "telegram", "wa", "both") {
		e["daily_summary_channel"] = "Choose Telegram, WhatsApp or both"
	}
	if x := v["alert_channel"]; x != "" && !oneOf(x, "telegram", "wa", "both") {
		e["alert_channel"] = "Choose Telegram, WhatsApp or both"
	}
	if x := v["trusted_proxies"]; x != "" {
		if _, ok := ParseTrustedProxies(x); !ok {
			e["trusted_proxies"] = "Enter IP addresses or CIDR ranges, separated by commas"
		}
	}
	if x := v["log_keep_days"]; x != "" && !inRange(x, 0, 3650) {
		e["log_keep_days"] = "Enter whole days, 0 to 3650. 0 keeps logs forever"
	}
	if x := v["radius_interim_interval"]; x != "" && !inRange(x, 0, 86400) {
		e["radius_interim_interval"] = "Enter whole seconds, 0 to 86400"
	}
	if x := v["backup_keep"]; x != "" && !inRange(x, 1, 1<<30) {
		e["backup_keep"] = "Enter a whole number, 1 or more"
	}
	if x, ok := v["radius_rest_allow"]; ok && !validAllowList(x) {
		e["radius_rest_allow"] = "Enter IP addresses or CIDR ranges, separated by commas"
	}
	if x := v["alert_nas_silent_minutes"]; x != "" && !inRange(x, 1, 1440) {
		e["alert_nas_silent_minutes"] = "Enter whole minutes, 1 to 1440"
	}
	if x := v["alert_mass_drop_min"]; x != "" && !inRange(x, 0, 100000) {
		e["alert_mass_drop_min"] = "Enter a whole number, 0 or more. 0 turns it off"
	}
	if x := v["alert_mass_drop_window"]; x != "" && !inRange(x, 1, 1440) {
		e["alert_mass_drop_window"] = "Enter whole minutes, 1 to 1440"
	}
	if x := v["expired_notify_minutes_before"]; x != "" && !inRange(x, 0, 1440) {
		e["expired_notify_minutes_before"] = "Enter whole minutes, 0 to 1440"
	}
	if x := v["reset_day"]; x != "" && !inRange(x, 1, 28) {
		e["reset_day"] = "Enter a day from 1 to 28"
	}
	if x := v["session_timeout_duration"]; x != "" && !inRange(x, 1, 525600) {
		e["session_timeout_duration"] = "Enter whole minutes, 1 or more"
	}
	if x := v["maintenance_date"]; x != "" {
		if _, err := time.Parse("2006-01-02", x); err != nil {
			e["maintenance_date"] = "Use a date like 2026-10-31"
		}
	}
	if x := v["wa_unique_per_account"]; x != "" && !oneOf(x, "yes", "no") {
		e["wa_unique_per_account"] = "Choose Yes or No"
	}
	if x := v["phone_otp_type"]; x != "" && !oneOf(x, "sms", "wa") {
		e["phone_otp_type"] = "Choose SMS or WhatsApp"
	}
	if x := v["customer_password_min"]; x != "" && !inRange(x, 4, 64) {
		e["customer_password_min"] = "Enter a whole number from 4 to 64"
	}
	if x := v["voucher_format"]; x != "" && !oneOf(x, "up", "low", "rand", "numbers") {
		e["voucher_format"] = "Choose one of the listed formats"
	}
	if x, ok := v["smtp_port"]; ok && x != "" && !inRange(x, 1, 65535) {
		e["smtp_port"] = "Enter a port from 1 to 65535"
	}
	if x := v["alt_wga_server_url"]; x != "" {
		if u, err := url.Parse(x); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			e["alt_wga_server_url"] = "Use an http or https URL"
		}
	}
	if x := v["wa_url"]; x != "" && v["alt_wga_server_url"] == "" && !(strings.Contains(x, "[number]") && strings.Contains(x, "[text]")) {
		e["wa_url"] = "URL must contain [number] and [text]"
	}
	if x := v["payment_gateway"]; x != "" && x != "tripay" && x != "duitku" {
		e["payment_gateway"] = "Choose one of the listed gateways"
	}
	if x := v["qris_payload"]; x != "" && payment.QRISValid(x) != nil {
		e["qris_payload"] = "Paste the full static QRIS text, the CRC must match"
	}
	if x := v["tripay_mode"]; x != "" && !oneOf(x, "sandbox", "production") {
		e["tripay_mode"] = "Choose sandbox or production"
	}
	if x := v["duitku_mode"]; x != "" && !oneOf(x, "sandbox", "production") {
		e["duitku_mode"] = "Choose sandbox or production"
	}
	if x := v["webhook_url"]; x != "" {
		if u, err := url.Parse(x); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			e["webhook_url"] = "Use an http or https URL"
		}
	}
	if x := v["voucher_redirect"]; x != "" {
		if u, err := url.Parse(x); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			e["voucher_redirect"] = "Use an http or https URL"
		}
	}
	for _, k := range []string{"extend_days", "minimum_transfer"} {
		if n, err := strconv.ParseInt(v[k], 10, 64); v[k] != "" && (err != nil || n < 0) {
			e[k] = "Enter a whole number, 0 or more"
		}
	}
	if x := v["hs_auth_method"]; x != "" && !oneOf(x, "pap", "chap") {
		e["hs_auth_method"] = "Choose PAP or CHAP"
	}
	if x, ok := v["default_plan_device"]; ok && !oneOf(x, "", "MikrotikHotspot", "MikrotikPppoe", "Dummy", "Radius") {
		e["default_plan_device"] = "Choose one of the listed devices"
	}
	return e
}
