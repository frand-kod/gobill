package web

// The isolir plan is a system plan (Settings > Isolir): it is hidden from the plan and bandwidth lists
// and the plan selects, and its speed is typed in the Isolir settings and kept in its bandwidth row.

import (
	"context"
	"fmt"
	"github.com/frand-kod/gobill/internal/settings"

	"github.com/frand-kod/gobill/internal/isolir"
)

// isolirIDs are the isolir plan and its bandwidth, 0 while the plan is not set up.
func (s *Server) isolirIDs(ctx context.Context) (planID, bwID int64) {
	if planID = isolir.StoredPlanID(ctx, s.queries); planID == 0 {
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
		down, up = rateSetting(b.RateDown, b.RateDownUnit), rateSetting(b.RateUp, b.RateUpUnit)
	}
	return p.Name, down, up
}

const isolirDefaultRate = "64k"

// rateRe is a rate typed in the Isolir settings: kbps with k, Mbps with M. A bare number is kbps.
// parseRate reads "64k" or "2M" as the number and the unit the bandwidth table stores.
func parseRate(x string) (int64, string, bool) { return settings.ParseRate(x) }

// rateSetting writes a bandwidth rate back the way the settings take it: "64k" or "2M".
func rateSetting(n int64, unit string) string {
	if unit == "Mbps" {
		return fmt.Sprint(n, "M")
	}
	return fmt.Sprint(n, "k")
}
