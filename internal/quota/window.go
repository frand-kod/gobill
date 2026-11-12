// Package quota holds the pure plan rules: login windows and MAC notation. It imports nothing internal.
package quota

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Rule is one "days from-to" rule of a schedule; days has bit time.Weekday set per day, from and
// to are minutes after midnight (to may be 1440). from > to wraps past midnight into the next day.
type Rule struct {
	days     uint8
	from, to int
}

var dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// ParseWindow reads a plan schedule: rules separated by ";" or a new line, each "DAYS HH:MM-HH:MM".
// DAYS is "*" (every day, also when left out), or names and ranges separated by commas:
// "Mon-Fri", "Sat,Sun", "Fri-Mon". A window that ends before it starts runs past midnight
// ("22:00-06:00") and belongs to the day it starts. An empty spec has no rules (always).
// Example: "Mon-Fri 08:00-17:00; Sat 09:00-12:00".
func ParseWindow(spec string) ([]Rule, error) {
	var out []Rule
	for _, part := range strings.FieldsFunc(spec, func(r rune) bool { return r == ';' || r == '\n' }) {
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		r := Rule{days: 0x7f}
		if len(f) == 2 {
			d, err := parseDays(f[0])
			if err != nil {
				return nil, err
			}
			r.days, f = d, f[1:]
		}
		if len(f) != 1 {
			return nil, fmt.Errorf("rule %q: want DAYS HH:MM-HH:MM", strings.TrimSpace(part))
		}
		from, to, ok := strings.Cut(f[0], "-")
		var err1, err2 error
		if r.from, err1 = parseHM(from); !ok || err1 != nil {
			return nil, fmt.Errorf("rule %q: bad time", strings.TrimSpace(part))
		}
		if r.to, err2 = parseHM(to); err2 != nil || r.from == r.to {
			return nil, fmt.Errorf("rule %q: bad time", strings.TrimSpace(part))
		}
		out = append(out, r)
	}
	return out, nil
}

func parseHM(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if !ok || e1 != nil || e2 != nil || hh < 0 || mm < 0 || mm > 59 || hh > 24 || hh == 24 && mm != 0 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	return hh*60 + mm, nil
}

func parseDays(s string) (uint8, error) {
	if s == "*" {
		return 0x7f, nil
	}
	var mask uint8
	for _, p := range strings.Split(strings.ToLower(s), ",") {
		a, b, isRange := strings.Cut(p, "-")
		da, ok1 := dayNames[a]
		db, ok2 := dayNames[b]
		if !isRange {
			db, ok2 = da, true
		}
		if !ok1 || !ok2 {
			return 0, fmt.Errorf("bad day %q", p)
		}
		for d := da; ; d = (d + 1) % 7 {
			mask |= 1 << d
			if d == db {
				break
			}
		}
	}
	return mask, nil
}

// InWindow reports whether t falls in one of the rules and when that window ends. No rules: always,
// with a zero end.
// ponytail: the end is that of the matching rule; two back-to-back rules cut the session at the seam
// and the customer logs in again at once.
func InWindow(rules []Rule, t time.Time) (in bool, end time.Time) {
	if len(rules) == 0 {
		return true, time.Time{}
	}
	d, m := int(t.Weekday()), t.Hour()*60+t.Minute()
	at := func(dayOffset, min int) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day()+dayOffset, min/60, min%60, 0, 0, t.Location())
	}
	for _, r := range rules {
		var e time.Time
		switch {
		case r.from < r.to && r.days>>d&1 == 1 && m >= r.from && m < r.to:
			e = at(0, r.to)
		case r.from > r.to && r.days>>d&1 == 1 && m >= r.from:
			e = at(1, r.to)
		case r.from > r.to && r.days>>((d+6)%7)&1 == 1 && m < r.to:
			e = at(0, r.to)
		default:
			continue
		}
		if !in || e.After(end) {
			in, end = true, e
		}
	}
	return in, end
}

// WindowLeft is the plan's login window at t (in t's zone): whether login is allowed and the seconds
// left in the window, 0 = no limit. An unreadable spec (the plan form validates it) is ignored.
func WindowLeft(spec string, t time.Time) (ok bool, secs int64) {
	rules, err := ParseWindow(spec)
	if err != nil {
		slog.Error("quota: bad login window, ignored", "spec", spec, "err", err)
		return true, 0
	}
	in, end := InWindow(rules, t)
	if !in || end.IsZero() {
		return in, 0
	}
	return true, max(int64(end.Sub(t).Seconds()), 1)
}

// BoostOn reports whether the boost window is active at t. No window: never.
func BoostOn(spec string, t time.Time) bool {
	rules, err := ParseWindow(spec)
	if err != nil || len(rules) == 0 {
		return false
	}
	in, _ := InWindow(rules, t)
	return in
}
