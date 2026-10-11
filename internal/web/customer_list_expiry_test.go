package web

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// The customer list's "Ends On" column: nearest active expiry, sortable, "Today" badge.
func TestCustomerListEndsOn(t *testing.T) {
	s, h, q, c := crudApp(t)
	ctx := t.Context()
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	plan, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "Ends10", Type: "PPPoE", Billing: "prepaid", Validity: 30, ValidityUnit: "Days",
		Device: "Radius", Enabled: 1, BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	loc := s.location()
	now := time.Now().In(loc)
	// last second of today in the app time zone, so the fixture is "today" at any wall-clock hour
	today := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc).Unix() - 1
	sub := func(name string, end int64, pending int64) {
		cu := newCust(t, q, name, "Ends "+name, "", "", "Active")
		if end == 0 {
			return
		}
		if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: cu.ID, PlanID: plan.ID, Type: "PPPoE",
			StartedAt: now.Unix(), ExpiresAt: end, PendingStart: pending}); err != nil {
			t.Fatal(err)
		}
	}
	sub("exp_a", now.Add(2*24*time.Hour).Unix(), 0)
	sub("exp_b", now.Add(5*24*time.Hour).Unix(), 0)
	sub("exp_c", 0, 0)                            // no subscription: "—", sorts last
	sub("exp_d", today, 0)                        // ends today: "Hari ini" badge
	sub("exp_p", now.Add(24*time.Hour).Unix(), 1) // start on first login: no date yet, "—"

	order := func(path string) []string {
		t.Helper()
		body := do(h, "GET", path, nil, c).Body.String()
		var names []string
		for _, u := range []string{"exp_a", "exp_b", "exp_c", "exp_d", "exp_p"} {
			if i := strings.Index(body, ">"+u+"<"); i >= 0 {
				names = append(names, u)
			}
		}
		// keep the rendered order
		for i := range names {
			for j := i + 1; j < len(names); j++ {
				if strings.Index(body, ">"+names[j]+"<") < strings.Index(body, ">"+names[i]+"<") {
					names[i], names[j] = names[j], names[i]
				}
			}
		}
		return names
	}

	body := do(h, "GET", "/admin/customers", nil, c).Body.String()
	for _, want := range []string{"Tanggal habis", "Hari ini", "—"} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if !strings.Contains(body, "sort=expires") {
		t.Error("Ends On header is not sortable")
	}

	// no expiry sorts last in both directions; among those the newest customer comes first (id DESC)
	wantAsc := []string{"exp_d", "exp_a", "exp_b", "exp_p", "exp_c"}
	if got := order("/admin/customers?sort=expires&dir=asc"); strings.Join(got, ",") != strings.Join(wantAsc, ",") {
		t.Errorf("asc: got %v, want %v", got, wantAsc)
	}
	wantDesc := []string{"exp_b", "exp_a", "exp_d", "exp_p", "exp_c"}
	if got := order("/admin/customers?sort=expires&dir=desc"); strings.Join(got, ",") != strings.Join(wantDesc, ",") {
		t.Errorf("desc: got %v, want %v", got, wantDesc)
	}
	// a sort key outside the whitelist falls back to the default order, no error
	if w := do(h, "GET", "/admin/customers?sort=expires_asc;--", nil, c); w.Code != 200 {
		t.Errorf("bad sort: %d", w.Code)
	}
	if n := strings.Count(body, "Hari ini"); n != 1 {
		t.Errorf("Hari ini badges: %d, want 1", n)
	}
}
