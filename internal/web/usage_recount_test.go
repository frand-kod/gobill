package web

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// The diagnosis flags a session log that holds more than the plan counts, Recount usage raises the
// counters to it, and a recount after pruning the log never lowers them.
func TestUsageRecount(t *testing.T) {
	s, h, q, c := crudApp(t)
	ctx, now := t.Context(), time.Now().Unix()
	bw, _ := q.CreateBandwidth(ctx, db.CreateBandwidthParams{Name: "b", RateDown: 1, RateDownUnit: "Mbps", RateUp: 1, RateUpUnit: "Mbps"})
	p, err := q.CreatePlan(ctx, db.CreatePlanParams{Name: "q", Type: "PPPoE", Billing: "prepaid", Validity: 30, ValidityUnit: "Days", Device: "Radius", Enabled: 1,
		BandwidthID: sql.NullInt64{Int64: bw.ID, Valid: true}, Limited: 1, LimitType: sql.NullString{String: "Data_Limit", Valid: true},
		DataLimit: sql.NullInt64{Int64: 100, Valid: true}, DataUnit: sql.NullString{String: "MB", Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	cu, err := q.CreateCustomer(ctx, db.CreateCustomerParams{Username: "rc1", PasswordHash: "h", Fullname: "R", ServiceType: "PPPoE", Status: "Active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: cu.ID, PlanID: p.ID, Type: "PPPoE", StartedAt: now - 3600, ExpiresAt: now + 86400}); err != nil {
		t.Fatal(err)
	}
	// 10 MB in the log that accounting never counted (an older version ran meanwhile)
	if err := q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "x", Username: "rc1", NasIp: "10.0.0.1",
		StartedAt: now - 600, UpdatedAt: now - 60, InputOctets: 10 << 20}); err != nil {
		t.Fatal(err)
	}
	behind := func() bool {
		t.Helper()
		subs, _ := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: cu.ID, Limit: 5})
		return s.diagnose(ctx, cu, subs, "").Item("Usage log") != nil
	}
	used := func() int64 {
		t.Helper()
		subs, _ := q.ListSubscriptionsByCustomer(ctx, db.ListSubscriptionsByCustomerParams{CustomerID: cu.ID, Limit: 5})
		return subs[0].UsedInOctets + subs[0].UsedOutOctets
	}
	if !behind() {
		t.Fatal("log ahead of the counters is not flagged")
	}
	page := do(h, "GET", fmt.Sprint("/admin/customers/", cu.ID), nil, c).Body.String()
	if !strings.Contains(page, fmt.Sprintf(`action="/admin/customers/%d/usage-recount"`, cu.ID)) {
		t.Fatal("no Recount usage action on the customer page")
	}
	recount := func() {
		t.Helper()
		if r := do(h, "POST", fmt.Sprintf("/admin/customers/%d/usage-recount", cu.ID), url.Values{}, c); r.Code != 303 {
			t.Fatalf("recount: %d", r.Code)
		}
	}
	recount()
	if used() != 10<<20 || behind() {
		t.Fatalf("after recount used %d, flagged %v", used(), behind())
	}
	if _, err := s.conn.ExecContext(ctx, "DELETE FROM radius_sessions"); err != nil {
		t.Fatal(err)
	}
	recount()
	if used() != 10<<20 || behind() {
		t.Fatalf("recount after pruning lowered the counters to %d", used())
	}
}
