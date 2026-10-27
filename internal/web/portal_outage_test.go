package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frand-kod/gobill/internal/db"
)

// The portal dashboard shows the outage banner to a customer of a mass drop, and hides it once they are back.
func TestPortalOutageBanner(t *testing.T) {
	e := billApp(t)
	portalCust(t, e, 0)
	ctx := t.Context()
	now := time.Now().Unix()
	stop := func(user string) {
		t.Helper()
		if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "old-" + user, Username: user, NasIp: "10.9.9.1",
			StartedAt: now - 7200, UpdatedAt: now - 60, StoppedAt: sql.NullInt64{Int64: now - 60, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	const banner = "Ada gangguan di area Anda, sedang ditangani"
	c, code := custLogin(t, e, "u1", "pw12345")
	if code != http.StatusSeeOther || c == nil {
		t.Fatalf("login: %d", code)
	}
	if w := do(e.h, "GET", "/portal", nil, c); strings.Contains(w.Body.String(), banner) {
		t.Fatal("no drop yet: no banner")
	}
	stop("u1")
	for i := 2; i <= 4; i++ {
		stop(fmt.Sprintf("u%d", i))
	}
	if w := do(e.h, "GET", "/portal", nil, c); strings.Contains(w.Body.String(), banner) {
		t.Fatal("4 dropped, default threshold 5: no banner")
	}
	stop("u5")
	if w := do(e.h, "GET", "/portal", nil, c); !strings.Contains(w.Body.String(), banner) {
		t.Fatal("5 dropped on the NAS: want the banner")
	}
	// u1 comes back online: the banner goes
	if err := e.q.UpsertRadiusSession(ctx, db.UpsertRadiusSessionParams{SessionID: "u1-back", Username: "u1", NasIp: "10.9.9.1",
		StartedAt: now - 10, UpdatedAt: now - 10}); err != nil {
		t.Fatal(err)
	}
	if w := do(e.h, "GET", "/portal", nil, c); strings.Contains(w.Body.String(), banner) {
		t.Fatal("u1 is back online: no banner")
	}
}
