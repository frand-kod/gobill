package view

import "strings"

// WaLink is the wa.me chat link for a phone number; numbers starting with 0 get the country code.
func WaLink(phone, cc string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if strings.HasPrefix(strings.TrimSpace(phone), "0") && cc != "" {
		d = cc + strings.TrimLeft(d, "0")
	}
	if len(d) < 8 {
		return ""
	}
	return "https://wa.me/" + d
}
