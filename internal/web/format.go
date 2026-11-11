package web

// Money, badge, byte/duration and thousands-separator formatting.

import (
	"sync/atomic"

	"fmt"
	"strconv"
	"strings"
	"time"

	moneyfmt "github.com/frand-kod/gobill/internal/money"
)

func humanBytes(n int64) string {
	num, unit := humanBytesParts(n)
	return num + " " + unit
}

// humanBytesParts is humanBytes split for the big-number style: ("112.61", "GB"), ("512", "B").
func humanBytesParts(n int64) (string, string) {
	f, u := float64(n), "B"
	for _, next := range []string{"KB", "MB", "GB", "TB"} {
		if f < 1024 {
			break
		}
		f, u = f/1024, next
	}
	if u == "B" {
		return fmt.Sprintf("%d", n), u
	}
	return fmt.Sprintf("%.2f", f), u
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

// money formats rupiah with the configured thousands separator: Rp 1.234.000, -Rp 1.000.
func money(n int64) string { return moneyfmt.Format(n, groupSep()) }

// badge maps a status value to its badge colour class.
func badge(v string) string {
	switch strings.ToLower(v) {
	case "active", "enable", "enabled", "unused", "yes", "online", "ok", "accepted":
		return "badge-ok"
	case "disable", "disabled", "banned", "suspended", "expired", "inactive", "no", "offline", "error", "rejected":
		return "badge-bad"
	case "limited", "secret lemah":
		return "badge-warn"
	}
	return "badge-muted"
}
