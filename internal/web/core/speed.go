package core

// Speed text and meter of a RADIUS session, shared by the admin live page and the portal usage card.

import (
	"fmt"
	"math"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
)

// SpeedMeter is the speed meter of a session: lit segments out of five, and their tone.
type SpeedMeter struct {
	Segs int    // 0 to 5
	Tone string // "ok", "warn" or "err"; "" when no segment is lit
}

// MeterOf maps the share of the plan's speed in use, in percent, to a meter. Winbox thresholds: green
// below 50%, yellow from 50% to 75%, red above 75%. Each segment is 20% of the plan.
func MeterOf(pct float64) SpeedMeter {
	segs := min(max(int(math.Round(pct/20)), 0), 5)
	tone := "ok"
	switch {
	case pct > 75:
		tone = "err"
	case pct >= 50:
		tone = "warn"
	}
	return SpeedMeter{Segs: segs, Tone: tone}
}

// Bars are the classes of the five segments: lit in the tone's colour, the rest in the line colour.
func (m SpeedMeter) Bars() []string {
	lit := map[string]string{"ok": "bg-meter-ok", "warn": "bg-meter-warn", "err": "bg-meter-err"}[m.Tone]
	out := make([]string, 5)
	for i := range out {
		out[i] = "bg-line"
		if i < m.Segs {
			out[i] = lit
		}
	}
	return out
}

// PlanSpeed is the active plan of a login with its speed limits in bits per second (0 = no limit).
type PlanSpeed struct {
	Name           string
	MaxDown, MaxUp float64
	DownLabel      string // "10 Mbps", as the plan's bandwidth is written
}

// NewPlanSpeed is the speed limits of a plan's bandwidth.
func NewPlanSpeed(name string, down int64, downUnit string, up int64, upUnit string) PlanSpeed {
	return PlanSpeed{Name: name, MaxDown: LimitBps(down, downUnit), MaxUp: LimitBps(up, upUnit), DownLabel: LimitLabel(down, downUnit)}
}

// PlanSpeeds maps each login name (username and pppoe_username) to its plan. The rows come newest expiry
// first, so the first plan of a login is the one that counts.
func PlanSpeeds(rows []db.ListActivePlanRatesRow) map[string]PlanSpeed {
	m := make(map[string]PlanSpeed, 2*len(rows))
	for _, p := range rows {
		ps := NewPlanSpeed(p.PlanName, p.RateDown, p.RateDownUnit, p.RateUp, p.RateUpUnit)
		for _, login := range []string{p.Username, p.PppoeUsername} {
			if _, dup := m[login]; login != "" && !dup {
				m[login] = ps
			}
		}
	}
	return m
}

// LimitBps is a speed limit in bits per second; 0 when the bandwidth has none.
func LimitBps(rate int64, unit string) float64 {
	if rate <= 0 {
		return 0
	}
	if strings.EqualFold(unit, "Mbps") {
		return float64(rate) * 1e6
	}
	return float64(rate) * 1e3
}

// LimitLabel is a speed limit as written in the plan, "10 Mbps"; "" when there is none.
func LimitLabel(rate int64, unit string) string {
	if rate <= 0 {
		return ""
	}
	if strings.EqualFold(unit, "Mbps") {
		return fmt.Sprintf("%d Mbps", rate)
	}
	return fmt.Sprintf("%d Kbps", rate)
}

// RateText is a rate for people: "3,2 Mbps" from 1 Mbps up, "512 kbps" below.
func RateText(bps float64) string {
	if bps >= 1e6 {
		return strings.Replace(fmt.Sprintf("%.1f Mbps", bps/1e6), ".", ",", 1)
	}
	return fmt.Sprintf("%.0f kbps", bps/1e3)
}

// SpeedOf is the speed text and the meter of a session, e.g. "↓3,2 Mbps / 10 Mbps ↑0,4 Mbps". The text
// is "—" until two interim updates exist. The meter uses the larger of the download and upload shares of
// the plan; it stays empty when the plan has no speed limit.
func SpeedOf(down, up float64, ok bool, plan PlanSpeed) (string, SpeedMeter) {
	if !ok {
		return "—", SpeedMeter{}
	}
	text := "↓" + RateText(down)
	if plan.MaxDown > 0 {
		text += " / " + plan.DownLabel
	}
	text += " ↑" + RateText(up)
	pct, limited := 0.0, false
	if plan.MaxDown > 0 {
		pct, limited = math.Max(pct, down/plan.MaxDown*100), true
	}
	if plan.MaxUp > 0 {
		pct, limited = math.Max(pct, up/plan.MaxUp*100), true
	}
	if !limited {
		return text, SpeedMeter{}
	}
	return text, MeterOf(pct)
}
