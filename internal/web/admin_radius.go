package web

// RADIUS live sessions: list and disconnect.

import (
	"fmt"
	"net/http"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/radius"
)

// sessRow is one session in a table. The tags are the live view's JSON; Speed and Bars are set on the live page only.
type sessRow struct {
	ID    int64    `json:"id"`
	User  string   `json:"user"`
	NAS   string   `json:"nas"`
	IP    string   `json:"ip"`
	MAC   string   `json:"mac"`
	Start string   `json:"start"`
	For   string   `json:"for"`
	Up    string   `json:"up"`
	Down  string   `json:"down"`
	Stale bool     `json:"stale"`
	Plan  string   `json:"plan"`
	Speed string   `json:"speed"`
	Bars  []string `json:"bars"` // the five meter segments, see meter.bars
}

// sessRows turns sessions into rows; the duration of a closed session ends at stopped_at.
func (s *Server) sessRows(ss []db.RadiusSession, now int64) []sessRow {
	out := make([]sessRow, 0, len(ss))
	for _, x := range ss {
		end, stale := now, false
		if x.StoppedAt.Valid {
			end = x.StoppedAt.Int64
		} else {
			stale = now-x.UpdatedAt > radius.StaleFor(x.NasIp)
		}
		// Input is what the NAS received from the user = upload.
		out = append(out, sessRow{ID: x.ID, User: x.Username, NAS: x.NasIp, IP: x.FramedIp, MAC: x.Mac,
			Start: s.ts(x.StartedAt), For: humanDur(end - x.StartedAt),
			Up: humanBytes(x.InputOctets), Down: humanBytes(x.OutputOctets), Stale: stale})
	}
	return out
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

// radiusDisconnectMany drops the selected sessions one by one through the same CoA path and reports
// "N disconnected, M failed". One activity log entry for the whole action.
func (s *Server) radiusDisconnectMany(w http.ResponseWriter, r *http.Request) {
	const back = "/admin/radius/sessions"
	ids, ok := parseIDs(r)
	if !ok {
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Select at least one row"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if len(ids) > maxDisconnectMany { // each CoA waits for its NAS, so the request must stay short
		s.sessions.Put(r.Context(), "error", s.catalog.T(s.language(), "Select at most 100 sessions"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	var okN, failN int
	var firstErr error
	for _, id := range ids {
		sess, err := s.queries.GetRadiusSession(r.Context(), id)
		if err == nil {
			err = radius.Disconnect(r.Context(), s.queries, s.SecretKey, s.CoAPort, sess)
		}
		if err != nil {
			failN++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		okN++
	}
	s.logActivity(r, "radius.disconnect_many", fmt.Sprintf("%d ok, %d failed", okN, failN))
	msg := fmt.Sprintf(s.catalog.T(s.language(), "%d disconnected, %d failed"), okN, failN)
	if okN == 0 {
		s.putFailure(r, msg, firstErr)
	} else {
		s.sessions.Put(r.Context(), "flash", msg)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

const maxDisconnectMany = 100
