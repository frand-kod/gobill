package web

// The isolir plan is a system plan (Settings > Isolir): it is hidden from the plan and bandwidth lists
// and the plan selects, and its speed is typed in the Isolir settings and kept in its bandwidth row.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/frand-kod/gobill/internal/db"
	"github.com/frand-kod/gobill/internal/isolir"
)

// isolirIDs are the isolir plan and its bandwidth, 0 while the plan is not set up.
func (s *Server) isolirIDs(ctx context.Context) (planID, bwID int64) {
	v, _ := s.queries.GetSetting(ctx, "isolir_plan_id")
	if planID = isolir.PlanID(v); planID == 0 {
		return 0, 0
	}
	if p, err := s.queries.GetPlan(ctx, planID); err == nil && p.BandwidthID.Valid {
		bwID = p.BandwidthID.Int64
	}
	return planID, bwID
}

// isolirSpeed is what the Isolir settings show: the plan name ("" = not set up yet) and its rates,
// from the isolir bandwidth row, or the default rate when there is none yet.
func (s *Server) isolirSpeed(ctx context.Context) (name, down, up string) {
	planID, bwID := s.isolirIDs(ctx)
	down, up = isolirDefaultRate, isolirDefaultRate
	if planID == 0 {
		return "", down, up
	}
	p, err := s.queries.GetPlan(ctx, planID)
	if err != nil {
		return "", down, up
	}
	if b, err := s.queries.GetBandwidth(ctx, bwID); err == nil {
		down, up = rateText(b.RateDown, b.RateDownUnit), rateText(b.RateUp, b.RateUpUnit)
	}
	return p.Name, down, up
}

const isolirDefaultRate = "64k"

// rateRe is a rate typed in the Isolir settings: kbps with k, Mbps with M. A bare number is kbps.
var rateRe = regexp.MustCompile(`^([1-9][0-9]{0,6})\s*([kKmM]?)$`)

// parseRate reads "64k" or "2M" as the number and the unit the bandwidth table stores.
func parseRate(x string) (n int64, unit string, ok bool) {
	m := rateRe.FindStringSubmatch(strings.TrimSpace(x))
	if m == nil {
		return 0, "", false
	}
	n, _ = strconv.ParseInt(m[1], 10, 64)
	if strings.EqualFold(m[2], "m") {
		return n, "Mbps", true
	}
	return n, "Kbps", true
}

// rateText writes a bandwidth rate back the way the settings take it: "64k" or "2M".
func rateText(n int64, unit string) string {
	if unit == "Mbps" {
		return fmt.Sprint(n, "M")
	}
	return fmt.Sprint(n, "k")
}

// saveIsolirRates writes the typed rates to the isolir bandwidth row when the plan exists. A missing
// or invalid rate is left alone; settingsErrors has already rejected invalid ones.
func saveIsolirRates(ctx context.Context, q *db.Queries, v map[string]string) error {
	raw, _ := q.GetSetting(ctx, "isolir_plan_id")
	p, err := q.GetPlan(ctx, isolir.PlanID(raw))
	if err != nil || !p.BandwidthID.Valid {
		return nil // no plan yet: it is made the next time Isolir is turned on
	}
	down, dunit, okDown := parseRate(v["isolir_rate_down"])
	up, uunit, okUp := parseRate(v["isolir_rate_up"])
	if !okDown || !okUp {
		return nil
	}
	b, err := q.GetBandwidth(ctx, p.BandwidthID.Int64)
	if err != nil {
		return nil
	}
	return q.UpdateBandwidth(ctx, db.UpdateBandwidthParams{Name: b.Name, RateDown: down, RateDownUnit: dunit,
		RateUp: up, RateUpUnit: uunit, Burst: b.Burst, ID: b.ID})
}
