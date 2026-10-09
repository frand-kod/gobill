package web

// Money, badge, byte/duration and thousands-separator formatting.

import (
	"sync/atomic"

	"fmt"
	"strconv"
	"strings"
	"time"
)

func humanBytes(n int64) string {
	f, u := float64(n), "B"
	for _, next := range []string{"KB", "MB", "GB", "TB"} {
		if f < 1024 {
			break
		}
		f, u = f/1024, next
	}
	if u == "B" {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.2f %s", f, u)
}

func humanDur(sec int64) string {
	d := time.Duration(max(sec, 0)) * time.Second
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", d/(24*time.Hour), d%(24*time.Hour)/time.Hour)
	}
	return fmt.Sprintf("%dh %02dm", d/time.Hour, d%time.Hour/time.Minute)
}

func bps(n int64, unit string) string {
	if unit == "Kbps" {
		return strconv.FormatInt(n*1000, 10)
	}
	return strconv.FormatInt(n*1000000, 10)
}

// thousandsSep is the digit group separator used by money. Settings load and save keep it current.
var thousandsSep atomic.Value // string

func setThousandsSep(v string) {
	if v == "" {
		v = "."
	}
	thousandsSep.Store(v)
}

func groupSep() string {
	if v, ok := thousandsSep.Load().(string); ok {
		return v
	}
	return "."
}

// money formats rupiah with dot thousands separators: Rp 1.234.000.
func money(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	d := strconv.FormatInt(n, 10)
	sep := groupSep()
	for i := len(d) - 3; i > 0; i -= 3 {
		d = d[:i] + sep + d[i:]
	}
	if neg {
		d = "-" + d
	}
	return "Rp " + d
}

// badge maps a status value to its badge colour class.
func badge(v string) string {
	switch strings.ToLower(v) {
	case "active", "enable", "enabled", "unused", "yes", "online", "ok":
		return "badge-ok"
	case "disable", "disabled", "banned", "suspended", "expired", "inactive", "no", "offline", "error":
		return "badge-bad"
	case "limited", "secret lemah":
		return "badge-warn"
	}
	return "badge-muted"
}
