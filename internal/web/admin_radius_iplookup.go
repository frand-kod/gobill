package web

// Who had IP X at time T: Framed-IP and start/stop range over the session history.

import (
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

const dtLocal = "2006-01-02T15:04"

func (s *Server) ipLookupFields(v, e map[string]string, result string) []field {
	out := section([]field{
		text("ip", "IP address", v, e).req(),
		text("at", "At (date and time)", v, e).as("datetime-local").hint("Empty = now. Session history is kept for the days set in Settings > Keep logs (days)."),
	}, "Who had this IP", "")
	if result != "" {
		out = append(section([]field{{Name: "result", Label: "Result", Type: "pre", Value: result}}, "Result", ""), out...)
	}
	return out
}

func (s *Server) ipLookupForm(w http.ResponseWriter, r *http.Request) {
	s.renderForm(w, r, 200, formPage{"Who had this IP", "/admin/radius/ip-lookup", "/admin/radius/sessions",
		s.ipLookupFields(map[string]string{"at": time.Now().In(s.location()).Format(dtLocal)}, nil, "")})
}

func (s *Server) ipLookupRun(w http.ResponseWriter, r *http.Request) {
	v := formVals(r, "ip", "at")
	t := func(k string) string { return s.catalog.T(s.language(), k) }
	e := map[string]string{}
	ip, err := netip.ParseAddr(strings.TrimSpace(v["ip"]))
	if err != nil {
		e["ip"] = "Enter a valid IP address"
	}
	at := time.Now()
	if v["at"] != "" {
		if at, err = time.ParseInLocation(dtLocal, v["at"], s.location()); err != nil {
			e["at"] = "Invalid value"
		}
	}
	var result string
	if len(e) == 0 {
		rows, err := s.queries.ListRadiusSessionsByIPAt(r.Context(), db.ListRadiusSessionsByIPAtParams{Ip: ip.String(), AtTs: at.Unix()})
		if err != nil {
			s.fail(w, "ip lookup", err)
			return
		}
		if len(rows) == 0 {
			result = t("No session had this IP at that time")
		}
		for _, x := range rows {
			stop := t("still online")
			if x.StoppedAt.Valid {
				stop = s.ts(x.StoppedAt.Int64)
			}
			result += x.Username + "  MAC " + x.Mac + "  NAS " + x.NasIp + "  " + s.ts(x.StartedAt) + " - " + stop + "\n"
		}
	}
	s.renderForm(w, r, 200, formPage{"Who had this IP", "/admin/radius/ip-lookup", "/admin/radius/sessions", s.ipLookupFields(v, e, strings.TrimRight(result, "\n"))})
}
