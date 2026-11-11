package web

// Live online sessions: the speed meter, the summary tiles and the JSON the page polls.
// The rates come from RADIUS Interim-Update: the counters of each open session are read on every refresh
// and kept in memory. Nothing here is stored in the database.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/web/core"
	"github.com/frand-kod/gobill/internal/web/view"
)

// liveTiles are the summary numbers. They cover all open sessions, not only the rows on the page.
type liveTiles struct {
	Online     int     `json:"online"`
	ActiveSubs int64   `json:"active_subs"`
	Down       float64 `json:"-"`
	Up         float64 `json:"-"`
	DownText   string  `json:"down_text"`
	UpText     string  `json:"up_text"`
}

// liveView is one refresh of the live page: the tiles and one page of rows.
type liveView struct {
	Tiles liveTiles      `json:"tiles"`
	Rows  []core.SessRow `json:"rows"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Per   int            `json:"per"`
	Nav   view.PageNav   `json:"-"`
}

// liveData is the live page: the view plus the filter and the state the script starts from.
type liveData struct {
	Q     string
	State string       // JSON of the tiles and the sparkline, for the first script refresh
	Blank core.SessRow // an empty row: the template the script clones for new sessions
	liveView
}

// liveTilesOf counts the summary numbers over all open sessions.
func (s *Server) liveTilesOf(ctx context.Context, open []db.RadiusSession) (liveTiles, error) {
	var t liveTiles
	var err error
	t.Online = len(open)
	if t.ActiveSubs, err = s.Queries.CountSubscriptionsByStatus(ctx, "active"); err != nil {
		return t, err
	}
	for _, x := range open {
		if down, up, ok := s.Live.Rate(x.ID); ok {
			t.Down += down
			t.Up += up
		}
	}
	t.DownText, t.UpText = core.RateText(t.Down), core.RateText(t.Up)
	return t, nil
}

// liveRows turns one page of open sessions into rows with their plan and speed.
func (s *Server) liveRows(ss []db.RadiusSession, now int64, plans map[string]core.PlanSpeed) []core.SessRow {
	rows := s.SessRows(ss, now)
	for i, x := range ss {
		plan := plans[x.Username]
		down, up, ok := s.Live.Rate(x.ID)
		var m core.SpeedMeter
		rows[i].Plan = plan.Name
		rows[i].Speed, m = core.SpeedOf(down, up, ok, plan)
		rows[i].Bars = m.Bars()
	}
	return rows
}

// liveLoad builds the live view for a request. The search and the page come from the query string.
func (s *Server) liveLoad(r *http.Request) (liveView, string, error) {
	ctx := r.Context()
	q, pg := view.Paging(r)
	now := time.Now().Unix()
	// All open sessions: they feed the tiles and the rate samples. The page is a filtered slice of them.
	open, err := s.Queries.SearchOpenRadiusSessions(ctx, db.SearchOpenRadiusSessionsParams{PageLimit: -1})
	if err != nil {
		return liveView{}, q, err
	}
	planRows, err := s.Queries.ListActivePlanRates(ctx)
	if err != nil {
		return liveView{}, q, err
	}
	plans := core.PlanSpeeds(planRows)
	s.Live.Refresh(open)
	var v liveView
	if v.Tiles, err = s.liveTilesOf(ctx, open); err != nil {
		return liveView{}, q, err
	}
	ss, err := s.Queries.SearchOpenRadiusSessions(ctx, db.SearchOpenRadiusSessionsParams{Q: q, PageLimit: pg.Limit(), PageOffset: pg.Offset()})
	if err != nil {
		return liveView{}, q, err
	}
	total, err := s.Queries.CountOpenRadiusSessions(ctx, q)
	if err != nil {
		return liveView{}, q, err
	}
	more := len(ss) > pg.Per
	if more {
		ss = ss[:pg.Per]
	}
	v.Rows = s.liveRows(ss, now, plans)
	v.Nav = view.NewPageNav(pg, total, len(v.Rows), more, func(page, per int) string {
		return view.PageHref("/admin/radius/sessions", url.Values{"q": {q}}, page, per)
	})
	v.Total, v.Page, v.Per = total, pg.Page, pg.Per
	return v, q, nil
}

func (s *Server) radiusSessions(w http.ResponseWriter, r *http.Request) {
	v, q, err := s.liveLoad(r)
	if err != nil {
		s.Fail(w, "list radius sessions", err)
		return
	}
	state, _ := json.Marshal(struct {
		Tiles liveTiles `json:"tiles"`
	}{v.Tiles})
	d := liveData{Q: q, State: string(state), Blank: core.SessRow{Bars: core.SpeedMeter{}.Bars()}, liveView: v}
	s.Render(w, r, http.StatusOK, "radius_sessions", Page{Title: "Online Sessions",
		Flash: s.Sessions.PopString(r.Context(), "flash"), Error: s.Sessions.PopString(r.Context(), "error"), Data: d})
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
