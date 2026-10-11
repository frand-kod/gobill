package web

// Live online sessions: the speed meter, the summary tiles, the 24 h sparkline and the JSON the page polls.
// The rates come from RADIUS Interim-Update: the counters of each open session are read on every refresh
// and kept in memory. Nothing here is stored in the database.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// liveSpark is the sparkline length: one online count every 5 minutes for 24 hours.
const liveSpark = 288

// liveSample is the octet counters of a session at one interim update.
type liveSample struct{ at, in, out int64 }

// liveRate keeps the last two interim updates of an open session. Its rate is the change between them.
type liveRate struct {
	n          int // samples seen, at most 2
	prev, last liveSample
}

// add records an interim update. An update that is not newer than the last one is not a new sample.
func (r *liveRate) add(s liveSample) {
	switch {
	case r.n == 0:
		r.last, r.n = s, 1
	case s.at > r.last.at:
		r.prev, r.last, r.n = r.last, s, 2
	}
}

// bps is the download and upload rate in bits per second between the last two updates. ok is false
// until two updates exist, or when the counters went backwards.
func (r *liveRate) bps() (down, up float64, ok bool) {
	if r.n < 2 {
		return 0, 0, false
	}
	dt := r.last.at - r.prev.at
	dOut, dIn := r.last.out-r.prev.out, r.last.in-r.prev.in
	if dt <= 0 || dOut < 0 || dIn < 0 {
		return 0, 0, false
	}
	return float64(dOut) * 8 / float64(dt), float64(dIn) * 8 / float64(dt), true
}

// liveMonitor is the in-memory state of the live view: the rate samples of the open sessions and the
// sparkline ring buffer. The zero value is ready to use.
type liveMonitor struct {
	mu    sync.Mutex
	rates map[int64]*liveRate // open sessions only: refresh drops the rest
	ring  [liveSpark]int      // online counts, the oldest is overwritten first
	n, at int                 // samples held, next write position
}

// refresh records the counters of the open sessions and forgets the sessions that are no longer open.
func (m *liveMonitor) refresh(open []db.RadiusSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[int64]*liveRate, len(open))
	for _, x := range open {
		r := m.rates[x.ID]
		if r == nil {
			r = &liveRate{}
		}
		r.add(liveSample{x.UpdatedAt, x.InputOctets, x.OutputOctets})
		keep[x.ID] = r
	}
	m.rates = keep
}

// rate is the current rate of an open session, see liveRate.bps.
func (m *liveMonitor) rate(id int64) (down, up float64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rates[id]
	if r == nil {
		return 0, 0, false
	}
	return r.bps()
}

// sample appends one online count to the sparkline.
func (m *liveMonitor) sample(online int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ring[m.at] = online
	m.at = (m.at + 1) % liveSpark
	if m.n < liveSpark {
		m.n++
	}
}

// series returns the sparkline, oldest first.
func (m *liveMonitor) series() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, 0, m.n)
	for i := m.n; i > 0; i-- {
		out = append(out, m.ring[(m.at-i+liveSpark)%liveSpark])
	}
	return out
}

// speedMeter is the speed meter of a session: lit segments out of five, and their tone.
type speedMeter struct {
	Segs int    // 0 to 5
	Tone string // "ok", "warn" or "err"; "" when no segment is lit
}

// meterOf maps the share of the plan's speed in use, in percent, to a meter. Winbox thresholds: green
// below 50%, yellow from 50% to 75%, red above 75%. Each segment is 20% of the plan.
func meterOf(pct float64) speedMeter {
	segs := min(max(int(math.Round(pct/20)), 0), 5)
	tone := "ok"
	switch {
	case pct > 75:
		tone = "err"
	case pct >= 50:
		tone = "warn"
	}
	return speedMeter{Segs: segs, Tone: tone}
}

// bars are the classes of the five segments: lit in the tone's colour, the rest in the line colour.
func (m speedMeter) bars() []string {
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

// planSpeed is the active plan of a login with its speed limits in bits per second (0 = no limit).
type planSpeed struct {
	Name           string
	MaxDown, MaxUp float64
	DownLabel      string // "10 Mbps", as the plan's bandwidth is written
}

// planSpeeds maps each login name (username and pppoe_username) to its plan. The rows come newest expiry
// first, so the first plan of a login is the one that counts.
func planSpeeds(rows []db.ListActivePlanRatesRow) map[string]planSpeed {
	m := make(map[string]planSpeed, 2*len(rows))
	for _, p := range rows {
		ps := planSpeed{Name: p.PlanName, MaxDown: limitBps(p.RateDown, p.RateDownUnit), MaxUp: limitBps(p.RateUp, p.RateUpUnit),
			DownLabel: limitLabel(p.RateDown, p.RateDownUnit)}
		for _, login := range []string{p.Username, p.PppoeUsername} {
			if _, dup := m[login]; login != "" && !dup {
				m[login] = ps
			}
		}
	}
	return m
}

// limitBps is a speed limit in bits per second; 0 when the bandwidth has none.
func limitBps(rate int64, unit string) float64 {
	if rate <= 0 {
		return 0
	}
	if strings.EqualFold(unit, "Mbps") {
		return float64(rate) * 1e6
	}
	return float64(rate) * 1e3
}

// limitLabel is a speed limit as written in the plan, "10 Mbps"; "" when there is none.
func limitLabel(rate int64, unit string) string {
	if rate <= 0 {
		return ""
	}
	if strings.EqualFold(unit, "Mbps") {
		return fmt.Sprintf("%d Mbps", rate)
	}
	return fmt.Sprintf("%d Kbps", rate)
}

// rateText is a rate for people: "3,2 Mbps" from 1 Mbps up, "512 kbps" below.
func rateText(bps float64) string {
	if bps >= 1e6 {
		return strings.Replace(fmt.Sprintf("%.1f Mbps", bps/1e6), ".", ",", 1)
	}
	return fmt.Sprintf("%.0f kbps", bps/1e3)
}

// speedOf is the speed text and the meter of a session, e.g. "↓3,2 Mbps / 10 Mbps ↑0,4 Mbps". The text
// is "—" until two interim updates exist. The meter uses the larger of the download and upload shares of
// the plan; it stays empty when the plan has no speed limit.
func speedOf(down, up float64, ok bool, plan planSpeed) (string, speedMeter) {
	if !ok {
		return "—", speedMeter{}
	}
	text := "↓" + rateText(down)
	if plan.MaxDown > 0 {
		text += " / " + plan.DownLabel
	}
	text += " ↑" + rateText(up)
	pct, limited := 0.0, false
	if plan.MaxDown > 0 {
		pct, limited = math.Max(pct, down/plan.MaxDown*100), true
	}
	if plan.MaxUp > 0 {
		pct, limited = math.Max(pct, up/plan.MaxUp*100), true
	}
	if !limited {
		return text, speedMeter{}
	}
	return text, meterOf(pct)
}

// liveCount is one line of a breakdown: a NAS or a plan and its online sessions.
type liveCount struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

// liveTiles are the summary numbers. They cover all open sessions, not only the rows on the page.
type liveTiles struct {
	Online      int         `json:"online"`
	ActiveSubs  int64       `json:"active_subs"`
	Logins      int64       `json:"logins_hour"`
	Disconnects int64       `json:"disconnects_hour"`
	Down        float64     `json:"-"`
	Up          float64     `json:"-"`
	DownText    string      `json:"down_text"`
	UpText      string      `json:"up_text"`
	ByNAS       []liveCount `json:"by_nas"`
	ByPlan      []liveCount `json:"by_plan"`
}

// liveView is one refresh of the live page: the tiles, the sparkline and one page of rows.
type liveView struct {
	Tiles liveTiles `json:"tiles"`
	Spark []int     `json:"spark"`
	Rows  []sessRow `json:"rows"`
	Total int64     `json:"total"`
	Page  int       `json:"page"`
	Per   int       `json:"per"`
	Nav   pageNav   `json:"-"`
}

// liveData is the live page: the view plus the filter and the state the script starts from.
type liveData struct {
	Q     string
	State string  // JSON of the tiles and the sparkline, for the first script refresh
	Blank sessRow // an empty row: the template the script clones for new sessions
	liveView
}

// liveBreakdown turns counts into lines, largest first.
func liveBreakdown(counts map[string]int) []liveCount {
	out := make([]liveCount, 0, len(counts))
	for name, n := range counts {
		out = append(out, liveCount{name, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// liveTilesOf counts the summary numbers over all open sessions.
func (s *Server) liveTilesOf(ctx context.Context, open []db.RadiusSession, plans map[string]planSpeed, now int64) (liveTiles, error) {
	var t liveTiles
	var err error
	t.Online = len(open)
	if t.ActiveSubs, err = s.queries.CountSubscriptionsByStatus(ctx, "active"); err != nil {
		return t, err
	}
	if t.Logins, err = s.queries.CountRadiusLoginsSince(ctx, now-3600); err != nil {
		return t, err
	}
	if t.Disconnects, err = s.queries.CountRadiusDisconnectsSince(ctx, now-3600); err != nil {
		return t, err
	}
	nas, byPlan := map[string]int{}, map[string]int{}
	for _, x := range open {
		if down, up, ok := s.live.rate(x.ID); ok {
			t.Down += down
			t.Up += up
		}
		nas[x.NasIp]++
		name := plans[x.Username].Name
		if name == "" {
			name = "—"
		}
		byPlan[name]++
	}
	t.DownText, t.UpText = rateText(t.Down), rateText(t.Up)
	t.ByNAS, t.ByPlan = liveBreakdown(nas), liveBreakdown(byPlan)
	return t, nil
}

// liveRows turns one page of open sessions into rows with their plan and speed.
func (s *Server) liveRows(ss []db.RadiusSession, now int64, plans map[string]planSpeed) []sessRow {
	rows := s.sessRows(ss, now)
	for i, x := range ss {
		plan := plans[x.Username]
		down, up, ok := s.live.rate(x.ID)
		var m speedMeter
		rows[i].Plan = plan.Name
		rows[i].Speed, m = speedOf(down, up, ok, plan)
		rows[i].Bars = m.bars()
	}
	return rows
}

// liveLoad builds the live view for a request. The search and the page come from the query string.
func (s *Server) liveLoad(r *http.Request) (liveView, string, error) {
	ctx := r.Context()
	q, pg := paging(r)
	now := time.Now().Unix()
	// All open sessions: they feed the tiles and the rate samples. The page is a filtered slice of them.
	open, err := s.queries.SearchOpenRadiusSessions(ctx, db.SearchOpenRadiusSessionsParams{PageLimit: -1})
	if err != nil {
		return liveView{}, q, err
	}
	planRows, err := s.queries.ListActivePlanRates(ctx)
	if err != nil {
		return liveView{}, q, err
	}
	plans := planSpeeds(planRows)
	s.live.refresh(open)
	var v liveView
	if v.Tiles, err = s.liveTilesOf(ctx, open, plans, now); err != nil {
		return liveView{}, q, err
	}
	ss, err := s.queries.SearchOpenRadiusSessions(ctx, db.SearchOpenRadiusSessionsParams{Q: q, PageLimit: pg.limit(), PageOffset: pg.offset()})
	if err != nil {
		return liveView{}, q, err
	}
	total, err := s.queries.CountOpenRadiusSessions(ctx, q)
	if err != nil {
		return liveView{}, q, err
	}
	more := len(ss) > pg.Per
	if more {
		ss = ss[:pg.Per]
	}
	v.Rows = s.liveRows(ss, now, plans)
	v.Nav = newPageNav(pg, total, len(v.Rows), more, func(page, per int) string {
		return pageHref("/admin/radius/sessions", url.Values{"q": {q}}, page, per)
	})
	v.Total, v.Page, v.Per = total, pg.Page, pg.Per
	v.Spark = s.live.series()
	return v, q, nil
}

func (s *Server) radiusSessions(w http.ResponseWriter, r *http.Request) {
	v, q, err := s.liveLoad(r)
	if err != nil {
		s.fail(w, "list radius sessions", err)
		return
	}
	state, _ := json.Marshal(struct {
		Tiles liveTiles `json:"tiles"`
		Spark []int     `json:"spark"`
	}{v.Tiles, v.Spark})
	d := liveData{Q: q, State: string(state), Blank: sessRow{Bars: speedMeter{}.bars()}, liveView: v}
	s.render(w, r, http.StatusOK, "radius_sessions", Page{Title: "Online Sessions",
		Flash: s.sessions.PopString(r.Context(), "flash"), Error: s.sessions.PopString(r.Context(), "error"), Data: d})
}

// radiusSessionsJSON is the live view as JSON, polled by the page. It needs the same login and takes the same filter.
func (s *Server) radiusSessionsJSON(w http.ResponseWriter, r *http.Request) {
	v, _, err := s.liveLoad(r)
	if err != nil {
		slog.Error("live sessions", "err", err)
		http.Error(w, "live sessions unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("live sessions json", "err", err)
	}
}

// SampleOnline records the online count for the sparkline. cmd/gobill runs it every 5 minutes.
func (s *Server) SampleOnline(ctx context.Context) error {
	n, err := s.queries.CountOpenRadiusSessions(ctx, "")
	if err != nil {
		return err
	}
	s.live.sample(int(n))
	return nil
}
