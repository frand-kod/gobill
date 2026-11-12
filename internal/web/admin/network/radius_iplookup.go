package network

// Who had IP X at time T: Framed-IP and start/stop range over the session history.

import (
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/web/httpx"

	"github.com/frand-kod/gobill/internal/db"
)

const dtLocal = "2006-01-02T15:04"

func (s *Handlers) ipLookupFields(v, e map[string]string, result string) []field {
	out := section([]field{
		text("ip", "IP address", v, e).Req(),
		text("at", "At (date and time)", v, e).As("datetime-local").WithHint("Prefilled with now. Empty also means now. Session history is kept for the days set in Settings > Keep logs (days)."),
	}, "Who had this IP", "")
	if result != "" {
		out = append(section([]field{{Name: "result", Label: "Result", Type: "pre", Value: result}}, "Result", ""), out...)
	}
	return out
}

func (s *Handlers) ipLookupForm(w http.ResponseWriter, r *http.Request) {
	s.RenderForm(w, r, 200, formPage{Heading: "Who had this IP", Action: "/admin/radius/ip-lookup", Cancel: "/admin/radius/sessions",
		Fields: s.ipLookupFields(map[string]string{"at": time.Now().In(s.Location()).Format(dtLocal)}, nil, "")})
}

func (s *Handlers) ipLookupRun(w http.ResponseWriter, r *http.Request) {
	v := httpx.FormVals(r, "ip", "at")
	e := map[string]string{}
	ip, err := netip.ParseAddr(strings.TrimSpace(v["ip"]))
	if err != nil {
		e["ip"] = "Enter a valid IP address"
	}
	at := time.Now()
	if v["at"] != "" {
		if at, err = time.ParseInLocation(dtLocal, v["at"], s.Location()); err != nil {
			e["at"] = "Invalid value"
		}
	}
	if len(e) > 0 {
		s.RenderForm(w, r, 200, formPage{Heading: "Who had this IP", Action: "/admin/radius/ip-lookup", Cancel: "/admin/radius/sessions", Fields: s.ipLookupFields(v, e, "")})
		return
	}
	rows, err := s.Queries.ListRadiusSessionsByIPAt(r.Context(), db.ListRadiusSessionsByIPAtParams{Ip: ip.String(), AtTs: at.Unix()})
	if err != nil {
		s.Fail(w, "ip lookup", err)
		return
	}
	// the result is a list page: one row per session, the table the generic form cannot draw
	lp := listPage{Heading: "Who had this IP", Base: "/admin/radius/ip-lookup", Cols: []string{"User", "MAC", "NAS", "Start", "Stop"},
		Links: []option{{Value: "/admin/radius/ip-lookup", Label: "Search again"}}}
	if len(rows) == 0 {
		lp.Note = "No session had this IP at that time"
	} else {
		lp.Note = ip.String() + " · " + s.TS(at.Unix())
	}
	for _, x := range rows {
		stop := s.T("still online")
		if x.StoppedAt.Valid {
			stop = s.TS(x.StoppedAt.Int64)
		}
		lp.Rows = append(lp.Rows, listRow{ID: x.ID, Cells: []string{x.Username, x.Mac, x.NasIp, s.TS(x.StartedAt), stop}})
	}
	s.RenderList(w, r, lp)
}
