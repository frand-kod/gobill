package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// The activity log shows times in the billing zone (Asia/Jakarta, UTC+7), not UTC.
func TestActivityLogShowsBillingZone(t *testing.T) {
	e := billApp(t)
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	e.s.Billing.Reload(nil, loc)
	if err := e.q.CreateActivityLog(t.Context(), db.CreateActivityLogParams{ActorType: "admin", Action: "Recharge", Description: "tz-marker"}); err != nil {
		t.Fatal(err)
	}
	// 1700000000 is 2023-11-14 22:13:20 UTC, which is 2023-11-15 05:13:20 in Jakarta.
	if _, err := e.s.conn.ExecContext(t.Context(), "UPDATE activity_logs SET created_at = 1700000000 WHERE description = 'tz-marker'"); err != nil {
		t.Fatal(err)
	}
	w := do(e.h, "GET", "/admin/logs", nil, e.c)
	if body := w.Body.String(); w.Code != http.StatusOK || !strings.Contains(body, "2023-11-15 05:13:20") {
		t.Fatalf("logs: %d\n%s", w.Code, body)
	}
}
