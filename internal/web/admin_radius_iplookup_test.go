package web

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

func TestIPLookupFindsSessionAtTime(t *testing.T) {
	s, h, q, c := crudApp(t)
	now := time.Now().Unix()
	add := func(sid, user, ip string, start, stop int64) {
		p := db.UpsertRadiusSessionParams{SessionID: sid, Username: user, NasIp: "10.0.0.1", FramedIp: ip, StartedAt: start, UpdatedAt: now}
		if stop != 0 {
			p.StoppedAt = sql.NullInt64{Int64: stop, Valid: true}
		}
		if err := q.UpsertRadiusSession(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "early-user", "10.9.0.5", now-7200, now-3600)
	add("b", "late-user", "10.9.0.5", now-3000, 0)
	add("c", "other-ip", "10.9.0.6", now-7200, 0)
	at := func(sec int64) string {
		w := do(h, "POST", "/admin/radius/ip-lookup", url.Values{"ip": {"10.9.0.5"}, "at": {time.Unix(sec, 0).In(s.location()).Format(dtLocal)}}, c)
		wantCode(t, w, 200, "lookup")
		return w.Body.String()
	}
	if b := at(now - 5000); !strings.Contains(b, "early-user") || strings.Contains(b, "late-user") || strings.Contains(b, "other-ip") {
		t.Errorf("t-5000: want only early-user")
	}
	if b := at(now - 120); !strings.Contains(b, "late-user") || strings.Contains(b, "early-user") {
		t.Errorf("t-120: want only late-user")
	}
	if b := at(now - 3300); strings.Contains(b, "early-user") || strings.Contains(b, "late-user") {
		t.Errorf("gap between the two sessions must find nobody")
	}
}
