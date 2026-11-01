package web

import (
	"net/url"
	"testing"

	"github.com/frand-kod/gobill/internal/db"
)

// The plan form saves idle timeout, login hours and boost, and refuses a bad schedule.
func TestRadiusPlanSchedule(t *testing.T) {
	e := billApp(t)
	form := func(extra url.Values) url.Values {
		f := url.Values{"name": {"Night"}, "type": {"Hotspot"}, "billing": {"prepaid"}, "price": {"1000"}, "validity": {"1"},
			"validity_unit": {"Days"}, "bandwidth_id": {itoa(e.bw)}, "device": {"Radius"}, "enabled": {"1"}}
		for k, v := range extra {
			f[k] = v
		}
		return f
	}
	wantCode(t, do(e.h, "POST", "/admin/plans", form(url.Values{"login_window": {"Mon 25:00-26:00"}}), e.c), 422, "bad login hours")
	wantCode(t, do(e.h, "POST", "/admin/plans", form(url.Values{"boost_window": {"* 00:00-06:00"}}), e.c), 422, "boost hours without bandwidth")
	wantCode(t, do(e.h, "POST", "/admin/plans", form(url.Values{"login_window": {"Mon-Fri 08:00-17:00"}, "idle_timeout": {"600"},
		"boost_window": {"* 00:00-06:00"}, "boost_bandwidth_id": {itoa(e.bw)}}), e.c), 303, "create")
	pl, _ := e.q.ListPlans(t.Context(), db.ListPlansParams{Limit: 10})
	if len(pl) != 1 || pl[0].LoginWindow != "Mon-Fri 08:00-17:00" || pl[0].IdleTimeout.Int64 != 600 ||
		pl[0].BoostWindow != "* 00:00-06:00" || pl[0].BoostBandwidthID.Int64 != e.bw {
		t.Fatalf("plans %+v", pl)
	}
}
