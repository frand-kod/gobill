package web

// RADIUS live sessions: list and disconnect.

import (
	"fmt"
	"net/http"

	"github.com/frand-kod/gobill/internal/web/httpx"

	"github.com/frand-kod/gobill/internal/radius"
)

func (s *Server) radiusDisconnect(w http.ResponseWriter, r *http.Request) {
	sess, err := s.Queries.GetRadiusSession(r.Context(), httpx.PathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := radius.Disconnect(r.Context(), s.Queries, s.SecretKey, s.CoAPort, sess); err != nil {
		s.putFailure(r, s.T("Disconnect failed")+". "+s.T(routerErr(err, msgNASOther)), err)
		http.Redirect(w, r, "/admin/radius/sessions", http.StatusSeeOther)
		return
	}
	s.done(w, r, "/admin/radius/sessions", "User disconnected", "radius.disconnect", sess.Username)
}

// radiusDisconnectMany drops the selected sessions one by one through the same CoA path and reports
// "N disconnected, M failed". One activity log entry for the whole action.
func (s *Server) radiusDisconnectMany(w http.ResponseWriter, r *http.Request) {
	const back = "/admin/radius/sessions"
	ids, ok := httpx.ParseIDs(r)
	if !ok {
		s.Sessions.Put(r.Context(), "error", s.T("Select at least one row"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if len(ids) > maxDisconnectMany { // each CoA waits for its NAS, so the request must stay short
		s.Sessions.Put(r.Context(), "error", s.T("Select at most 100 sessions"))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	var okN, failN int
	var firstErr error
	for _, id := range ids {
		sess, err := s.Queries.GetRadiusSession(r.Context(), id)
		if err == nil {
			err = radius.Disconnect(r.Context(), s.Queries, s.SecretKey, s.CoAPort, sess)
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
	msg := fmt.Sprintf(s.T("%d disconnected, %d failed"), okN, failN)
	if okN == 0 {
		s.putFailure(r, msg, firstErr)
	} else {
		s.Sessions.Put(r.Context(), "flash", msg)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

const maxDisconnectMany = 100
