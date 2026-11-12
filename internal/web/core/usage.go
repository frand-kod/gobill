package core

// Usage charts, countdown words and the customer usage card, shared by the portal and the admin pages.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/view"
)

// DayStart is local midnight of the day of now.
func DayStart(now int64, loc *time.Location) time.Time {
	t := time.Unix(now, 0).In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// WeekSince is the first day of the chart: six days before today.
func WeekSince(now int64, loc *time.Location) string {
	return DayStart(now, loc).AddDate(0, 0, -6).Format("2006-01-02")
}

// DayUse is one day of the chart: the octets moved that day (radius_usage_daily).
type DayUse struct {
	Label    string
	Down, Up int64 // Down: octets sent to the customer; Up: octets the customer sent
	Today    bool
}

// DayTotal is one day's octets (radius_usage_daily, summed over the users shown). Day is YYYY-MM-DD.
type DayTotal struct {
	Day           string
	Input, Output int64
}

// DailyUsage lays the rows out as the last n days, today last.
func DailyUsage(rows []DayTotal, n int, now int64, loc *time.Location) []DayUse {
	days := DailyRange(rows, DayStart(now, loc).AddDate(0, 0, -(n-1)), n)
	days[n-1].Today = true
	return days
}

// DailyRange lays the rows out as n days from first (a local midnight).
func DailyRange(rows []DayTotal, first time.Time, n int) []DayUse {
	days := make([]DayUse, n)
	idx := make(map[string]int, n)
	for i := range days {
		d := first.AddDate(0, 0, i).Format("2006-01-02")
		days[i] = DayUse{Label: d}
		idx[d] = i
	}
	for _, x := range rows {
		if i, ok := idx[x.Day]; ok {
			days[i].Down += x.Output
			days[i].Up += x.Input
		}
	}
	return days
}

// DayBar is one column of the usage chart: the upload stacked on the download. DownPct and UpPct are
// percentages (0..100) of the busiest day's total, so the busiest day fills the chart.
type DayBar struct {
	Label    string
	Tick     string // axis label, "" where the axis has none
	Down, Up string // the day's totals, shown on hover or tap
	DownPct  int
	UpPct    int
	Today    bool
}

// ChartBars scales the days to the chart: the busiest day is 100%. Up to 10 days label every bar; more
// days label every fifth day and the last one.
func ChartBars(days []DayUse) []DayBar {
	var top int64 = 1
	for _, d := range days {
		top = max(top, d.Down+d.Up)
	}
	out := make([]DayBar, len(days))
	for i, d := range days {
		// the stack is rounded as a whole, so the busiest day comes to exactly 100
		downPct := int(d.Down * 100 / top)
		tick := d.Label
		if t, err := time.Parse("2006-01-02", d.Label); err == nil {
			tick = t.Format("02/01") // a full date per bar does not fit a phone; the tooltip keeps it
		}
		if len(days) > 10 && i%5 != 0 && i != len(days)-1 {
			tick = ""
		}
		out[i] = DayBar{Label: d.Label, Tick: tick, Down: view.HumanBytes(d.Down), Up: view.HumanBytes(d.Up),
			DownPct: downPct, UpPct: int((d.Down+d.Up)*100/top) - downPct, Today: d.Today}
	}
	return out
}

// PWords are the countdown and duration words, translated. The Alpine ticker gets the same ones.
type PWords struct {
	Days, Hours, Minutes, Left, Done string
}

func (s *Core) PTexts() PWords {
	return PWords{s.T("days"), s.T("hours"), s.T("minutes"), s.T("left"), s.T("Ended")}
}

// JSON is the words as a JSON object for the ticker's x-data.
func (w PWords) JSON() string {
	b, _ := json.Marshal(map[string]string{"days": w.Days, "hours": w.Hours, "minutes": w.Minutes, "left": w.Left, "done": w.Done})
	return string(b)
}

// span: "3 hari 4 jam", "2 jam 15 menit", "45 menit". The ticker's gbSpan in usage.js does the same.
func (w PWords) Span(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	d, h, m := sec/86400, sec%86400/3600, sec%3600/60
	switch {
	case d > 0:
		if h > 0 {
			return fmt.Sprintf("%d %s %d %s", d, w.Days, h, w.Hours)
		}
		return fmt.Sprintf("%d %s", d, w.Days)
	case h > 0:
		if m > 0 {
			return fmt.Sprintf("%d %s %d %s", h, w.Hours, m, w.Minutes)
		}
		return fmt.Sprintf("%d %s", h, w.Hours)
	case sec > 0 && m == 0:
		m = 1 // under a minute still reads as one minute
	}
	return fmt.Sprintf("%d %s", m, w.Minutes)
}

// countdown: "3 hari 4 jam lagi", or the done word once the time is up.
func (w PWords) Countdown(sec int64) string {
	if sec <= 0 {
		return w.Done
	}
	return w.Span(sec) + " " + w.Left
}

// LoginNames: the RADIUS user names of the customer (username, and the PPPoE name when it differs).
func LoginNames(c db.Customer) []string {
	names := []string{c.Username}
	if c.PppoeUsername != "" && c.PppoeUsername != c.Username {
		names = append(names, c.PppoeUsername)
	}
	return names
}

// Names2 are the two login names the queries take: the same name twice when there is only one.
func Names2(names []string) (string, string) {
	return names[0], names[len(names)-1]
}

// UseWin is one window of the customer's daily usage chart: its total and its bars.
type UseWin struct {
	Total string
	Bars  []DayBar
}

// UseLast is the newest session in one line: start, duration and the sum of both directions.
type UseLast struct{ Start, For, Total string }

type RadiusUsage struct {
	Total, Unit string    // data since the plan started, the big number and its unit
	Empty       bool      // no daily usage and no session recorded
	Week, Month UseWin    // the 7 and 30 day charts, built from one query
	Last        *UseLast  // nil when there is no session
	Rows        []SessRow // the last 10 sessions, under Session history
}

// useWindow sums one window of the daily chart and lays out its bars.
func useWindow(days []DayUse) UseWin {
	var n int64
	for _, d := range days {
		n += d.Down + d.Up
	}
	return UseWin{Total: view.HumanBytes(n), Bars: ChartBars(days)}
}

// RadiusUsage: bytes since the active subscription started, the daily chart for 7 and 30 days, and the
// last 10 sessions.
func (s *Core) RadiusUsage(ctx context.Context, c db.Customer) (*RadiusUsage, error) {
	u := &RadiusUsage{}
	var total int64
	if p, err := s.Queries.GetRadiusPlan(ctx, c.ID); err == nil {
		total = p.UsedInOctets + p.UsedOutOctets
	}
	u.Total, u.Unit = view.HumanBytesParts(total)
	now, loc := time.Now().Unix(), s.Location()
	n1, n2 := Names2(LoginNames(c))
	rows, err := s.Queries.ListRadiusUsageDaily(ctx, db.ListRadiusUsageDailyParams{Name1: n1, Name2: n2, Since: DayStart(now, loc).AddDate(0, 0, -29).Format("2006-01-02")})
	if err != nil {
		return nil, err
	}
	ss, err := s.Queries.ListRecentRadiusSessionsByUser(ctx, c.Username)
	if err != nil {
		return nil, err
	}
	u.Empty = len(rows) == 0 && len(ss) == 0
	totals := make([]DayTotal, len(rows))
	for i, x := range rows {
		totals[i] = DayTotal{Day: x.Day, Input: x.Input, Output: x.Output}
	}
	u.Week = useWindow(DailyUsage(totals, 7, now, loc))
	u.Month = useWindow(DailyUsage(totals, 30, now, loc))
	if len(ss) > 0 {
		r := s.SessRows(ss[:1], now)[0]
		u.Last = &UseLast{Start: r.Start, For: r.For, Total: view.HumanBytes(ss[0].InputOctets + ss[0].OutputOctets)}
	}
	u.Rows = s.SessRows(ss, now)
	return u, nil
}
