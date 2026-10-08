package web

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/nuxbill-go/internal/db"
)

func TestDashboardWidgets(t *testing.T) {
	e := billApp(t)
	ctx := t.Context()
	p := e.plan(t, "p1", "PPPoE", 25000)
	now := time.Now().Unix()
	if _, err := e.q.CreateSubscription(ctx, db.CreateSubscriptionParams{CustomerID: e.cust.ID, PlanID: p.ID, RouterID: sql.NullInt64{Int64: e.rt, Valid: true},
		Type: "PPPoE", StartedAt: now, ExpiresAt: now + 3600, Method: "Cash"}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertSetting(ctx, db.UpsertSettingParams{Key: "expiry_last_run", Value: "2026-10-08 02:00:00"}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.CreateActivityLog(ctx, db.CreateActivityLogParams{ActorType: "admin", Action: "Recharge", Description: "widget-log-marker"}); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/admin", nil, e.c)
	body := w.Body.String()
	for _, want := range []string{
		`href="/admin/customers/` + strconv.FormatInt(e.cust.ID, 10) + `">u1<`, // expiring list links to the customer
		`data-chart="pie"`, `data-values="[1,0]"`,
		`2026-10-08 02:00:00`, // job monitor reads expiry_last_run
		`widget-log-marker`,   // activity log
		`>p1<`,                // voucher stock row (plan with zero vouchers)
	} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q (status %d)", want, w.Code)
		}
	}
}
