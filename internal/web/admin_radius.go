package web

// RADIUS live sessions: list and disconnect.

import (
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
	"time"
)

type sessRow struct {
	ID                             int64
	User, NAS, IP, MAC, Start, For string
	Up, Down                       string
	Stale                          bool
}

// sessRows turns sessions into rows; the duration of a closed session ends at stopped_at.
func (s *Server) sessRows(ss []db.RadiusSession, now int64) []sessRow {
	out := make([]sessRow, 0, len(ss))
	for _, x := range ss {
		end, stale := now, false
		if x.StoppedAt.Valid {
			end = x.StoppedAt.Int64
		} else {
			stale = now-x.UpdatedAt > radius.StaleAfter
		}
		// Input is what the NAS received from the user = upload.
		out = append(out, sessRow{x.ID, x.Username, x.NasIp, x.FramedIp, x.Mac, s.ts(x.StartedAt), humanDur(end - x.StartedAt),
			humanBytes(x.InputOctets), humanBytes(x.OutputOctets), stale})
	}
	return out
}

func (s *Server) radiusSessions(w http.ResponseWriter, r *http.Request) {
	q, page, limit, off := paging(r)
	ss, err := s.queries.SearchOpenRadiusSessions(r.Context(), db.SearchOpenRadiusSessionsParams{Q: q, PageLimit: limit, PageOffset: off})
	if err != nil {
		s.fail(w, "list radius sessions", err)
		return
	}
	d := struct {
		Q          string
		Rows       []sessRow
		Prev, Next int
	}{Q: q}
	if len(ss) > perPage {
		ss, d.Next = ss[:perPage], page+1
	}
	if page > 1 {
		d.Prev = page - 1
	}
	d.Rows = s.sessRows(ss, time.Now().Unix())
	s.render(w, r, http.StatusOK, "radius_sessions", Page{Title: "Online Sessions",
		Flash: s.sessions.PopString(r.Context(), "flash"), Error: s.sessions.PopString(r.Context(), "error"), Data: d})
}

func (s *Server) radiusDisconnect(w http.ResponseWriter, r *http.Request) {
	sess, err := s.queries.GetRadiusSession(r.Context(), pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := radius.Disconnect(r.Context(), s.queries, s.SecretKey, s.CoAPort, sess); err != nil {
		s.putFailure(r, s.catalog.T(s.language(), "Disconnect failed")+". "+s.catalog.T(s.language(), routerErr(err, msgNASOther)), err)
		http.Redirect(w, r, "/admin/radius/sessions", http.StatusSeeOther)
		return
	}
	s.done(w, r, "/admin/radius/sessions", "User disconnected", "radius.disconnect", sess.Username)
}
