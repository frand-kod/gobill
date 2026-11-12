package portal

// Customer live card, daily usage chart and connection history. Everything is read for the logged-in
// customer's own login names (loginNames) only.

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/view"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
)

// PLive is the live card: the open session and its speed when online, else the last time the device was seen.
// The dashboard renders it and /portal/usage.json returns it, polled every 15 s.
type PLive struct {
	Online bool     `json:"online"`
	Seen   string   `json:"seen"`   // offline: last time the device was seen, "" when never
	Device string   `json:"device"` // masked MAC
	IP     string   `json:"ip"`
	Since  string   `json:"since"`
	For    string   `json:"for"`
	Used   string   `json:"used"`  // bytes of this session, up and down
	Down   string   `json:"down"`  // current download rate, "—" until two interim updates
	Up     string   `json:"up"`    // current upload rate
	Limit  string   `json:"limit"` // the plan's download limit as written, "" when none
	Label  string   `json:"label"` // the meter's text for screen readers
	Bars   []string `json:"bars"`  // the five meter segments, see core.SpeedMeter.bars
	Pct    int      `json:"pct"`   // download rate as a share of the plan's, 0..100; the gauge's needle
}

// JSON is the live card for the x-data of the card.
func (l PLive) JSON() string {
	b, _ := json.Marshal(l)
	return string(b)
}

// pLiveOf reads the customer's open session. The speed uses the same samples, plan limits and meter as the
// admin live page (liveMonitor, speedOf).
func (s *Handlers) pLiveOf(ctx context.Context, c db.Customer, now int64, w core.PWords) (PLive, error) {
	l := PLive{Down: "—", Up: "—", Label: "—", Bars: core.SpeedMeter{}.Bars()}
	var open []db.RadiusSession
	var last *db.RadiusSession
	for _, n := range core.LoginNames(c) {
		ss, err := s.Queries.ListOpenRadiusSessionsByUser(ctx, n)
		if err != nil {
			return PLive{}, err
		}
		open = append(open, ss...)
		recent, err := s.Queries.ListRecentRadiusSessionsByUser(ctx, n)
		if err != nil {
			return PLive{}, err
		}
		if len(recent) > 0 && (last == nil || recent[0].StartedAt > last.StartedAt) {
			last = &recent[0]
		}
	}
	if last != nil {
		l.Seen = s.TS(max(last.UpdatedAt, last.StoppedAt.Int64))
	}
	var cur *db.RadiusSession
	for i := range open {
		if cur == nil || open[i].UpdatedAt > cur.UpdatedAt {
			cur = &open[i]
		}
	}
	if cur == nil || now-cur.UpdatedAt > radius.StaleFor(cur.NasIp) {
		return l, nil
	}
	l.Online = true
	l.IP, l.Device = cur.FramedIp, maskMAC(cur.Mac)
	l.Since, l.For = s.TS(cur.StartedAt), w.Span(now-cur.StartedAt)
	l.Used = view.HumanBytes(cur.InputOctets + cur.OutputOctets)
	var plan core.PlanSpeed
	if pl, err := s.Queries.GetRadiusPlan(ctx, c.ID); err == nil {
		plan = core.NewPlanSpeed(pl.PlanName, pl.RateDown.Int64, pl.RateDownUnit.String, pl.RateUp.Int64, pl.RateUpUnit.String)
	}
	s.Live.Observe(*cur, now)
	down, up, ok := s.Live.Rate(cur.ID)
	var m core.SpeedMeter
	l.Label, m = core.SpeedOf(down, up, ok, plan)
	l.Bars, l.Limit = m.Bars(), plan.DownLabel
	if ok {
		l.Down, l.Up = core.RateText(down), core.RateText(up)
		if plan.MaxDown > 0 {
			l.Pct = min(int(math.Round(down/plan.MaxDown*100)), 100)
		}
	}
	return l, nil
}

// pUsageJSON is the live card as JSON. Only the logged-in customer's sessions are read.
func (s *Handlers) pUsageJSON(w http.ResponseWriter, r *http.Request) {
	l, err := s.pLiveOf(r.Context(), *customerFrom(r), time.Now().Unix(), s.PTexts())
	if err != nil {
		slog.Error("portal usage json", "err", err)
		http.Error(w, "usage unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(l); err != nil {
		slog.Error("portal usage json", "err", err)
	}
}

// pConnDay is one day of the connection history: its sessions, newest first, and the day's totals on this page.
type pConnDay struct {
	Label    string
	Down, Up string
	Rows     []pSess
	key      string
	down, up int64
}

// pConnPage is one page of the connection history, grouped by day.
type pConnPage struct {
	Days []pConnDay
	Nav  view.PageNav
}

// pConnHistory is the customer's connection history, one page at a time. A day that continues on the next
// page shows as two groups; ponytail: group in SQL if that is ever confusing.
func (s *Handlers) pConnHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, pg := view.Paging(r)
	names := core.LoginNames(*customerFrom(r))
	n1, n2 := core.Names2(names)
	ss, err := s.Queries.ListRadiusSessionsByUsers(ctx, db.ListRadiusSessionsByUsersParams{Name1: n1, Name2: n2, PageLimit: pg.Limit(), PageOffset: pg.Offset()})
	if err != nil {
		s.Fail(w, "portal connection history", err)
		return
	}
	total, err := s.Queries.CountRadiusSessionsByUsers(ctx, db.CountRadiusSessionsByUsersParams{Name1: n1, Name2: n2})
	if err != nil {
		s.Fail(w, "count portal connection history", err)
		return
	}
	more := len(ss) > pg.Per
	if more {
		ss = ss[:pg.Per]
	}
	now, loc, w2 := time.Now().Unix(), s.Location(), s.PTexts()
	var out pConnPage
	for _, x := range ss {
		key := time.Unix(x.StartedAt, 0).In(loc).Format("2006-01-02")
		if len(out.Days) == 0 || out.Days[len(out.Days)-1].key != key {
			out.Days = append(out.Days, pConnDay{Label: time.Unix(x.StartedAt, 0).In(loc).Format("2006-01-02"), key: key})
		}
		day := &out.Days[len(out.Days)-1]
		day.Rows = append(day.Rows, s.pSession(x, now, w2))
		day.down += x.OutputOctets
		day.up += x.InputOctets
	}
	for i := range out.Days {
		out.Days[i].Down, out.Days[i].Up = view.HumanBytes(out.Days[i].down), view.HumanBytes(out.Days[i].up)
	}
	out.Nav = view.NewPageNav(pg, total, len(ss), more, func(page, per int) string {
		return view.PageHref("/portal/connection", url.Values{}, page, per)
	})
	s.Prender(w, r, http.StatusOK, "p_connection", core.Page{Title: "Connection History", Data: out})
}
