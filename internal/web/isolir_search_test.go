package web

import (
	"fmt"
	"testing"
)

// The header search lists plans, but not the isolir plan.
func TestIsolirPlanNotInHeaderSearch(t *testing.T) {
	h, c, _, _, isoID, gold, _ := isolirPlanSetup(t)
	if got := searchURLs(t, h, c, "isolir"); hasURL(got, fmt.Sprintf("/admin/plans/%d/edit", isoID)) {
		t.Errorf("header search lists the isolir plan: %v", got)
	}
	if got := searchURLs(t, h, c, "gold"); !hasURL(got, fmt.Sprintf("/admin/plans/%d/edit", gold.ID)) {
		t.Errorf("header search misses Gold: %v", got)
	}
}
