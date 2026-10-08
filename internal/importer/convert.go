package importer

import (
	"fmt"
	"strings"
	"time"
)

// ParsePrice turns an old varchar price ("Rp. 10.000", "10,000", "1000.50", "10.000,50")
// into whole rupiah, rounding half up. A lone separator followed by exactly 3 digits is a
// thousands separator.
func ParsePrice(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '.' || r == ',' {
			b.WriteRune(r)
		}
	}
	s = b.String()
	if s == "" || strings.Trim(s, ".,") == "" {
		return 0, fmt.Errorf("no number in price")
	}
	if neg {
		return 0, fmt.Errorf("negative price")
	}
	dec := -1 // index of the decimal separator
	if i := strings.LastIndexAny(s, ".,"); i >= 0 {
		sep := s[i : i+1]
		other := map[string]string{".": ",", ",": "."}[sep]
		switch {
		case strings.Contains(s, other): // both kinds: the last one is the decimal point
			dec = i
		case strings.Count(s, sep) == 1 && len(s)-i-1 != 3:
			dec = i
		}
	}
	whole, frac := s, ""
	if dec >= 0 {
		whole, frac = s[:dec], s[dec+1:]
	}
	whole = strings.NewReplacer(".", "", ",", "").Replace(whole)
	var n int64
	for _, c := range whole {
		n = n*10 + int64(c-'0')
		if n > 1<<50 {
			return 0, fmt.Errorf("price too large")
		}
	}
	if frac != "" && frac[0] >= '5' && frac[0] <= '9' {
		n++
	}
	return n, nil
}

// ParseWhen combines an old date and time column in loc into unix seconds.
func ParseWhen(date, clock string, loc *time.Location) (int64, error) {
	date, clock = strings.TrimSpace(date), strings.TrimSpace(clock)
	if i := strings.IndexAny(date, " T"); i > 0 { // a datetime column
		if clock == "" {
			clock = date[i+1:]
		}
		date = date[:i]
	}
	if clock == "" {
		clock = "00:00:00"
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", date+" "+clock, loc)
	if err != nil {
		return 0, fmt.Errorf("bad date %q %q", date, clock)
	}
	return t.Unix(), nil
}

// SubStatus maps tbl_user_recharges.status ('on'/'off').
func SubStatus(s string) string {
	if strings.EqualFold(s, "on") {
		return "active"
	}
	return "expired"
}

// PlanType maps the old enum (PPPOE -> PPPoE); VPN has no equivalent.
func PlanType(s string) (string, error) {
	switch strings.ToLower(s) {
	case "hotspot":
		return "Hotspot", nil
	case "pppoe":
		return "PPPoE", nil
	case "balance":
		return "Balance", nil
	}
	return "", fmt.Errorf("unsupported type %q", s)
}
