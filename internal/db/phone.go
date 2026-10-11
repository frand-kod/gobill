package db

import "strings"

// CanonicalPhone is the form a phone number is compared in: digits only, with a leading 0 (the
// national form) replaced by 62, so "0812-3456-789", "+62 812 3456 789" and "628123456789" are one
// number. Phones are stored as typed, so comparisons normalise both sides; no migration needed.
func CanonicalPhone(p string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, p)
	switch {
	case d == "0":
		return ""
	case strings.HasPrefix(d, "0"):
		return "62" + d[1:]
	}
	return d
}
